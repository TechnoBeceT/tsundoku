package enginehost

import io.github.oshai.kotlinlogging.KotlinLogging
import org.objectweb.asm.ClassReader
import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Opcodes
import org.objectweb.asm.Type
import org.objectweb.asm.tree.AbstractInsnNode
import org.objectweb.asm.tree.ClassNode
import org.objectweb.asm.tree.FieldInsnNode
import org.objectweb.asm.tree.InsnNode
import org.objectweb.asm.tree.MethodInsnNode
import org.objectweb.asm.tree.MethodNode
import org.objectweb.asm.tree.TypeInsnNode
import org.objectweb.asm.tree.VarInsnNode
import org.objectweb.asm.tree.analysis.Analyzer
import org.objectweb.asm.tree.analysis.Frame
import org.objectweb.asm.tree.analysis.SourceInterpreter
import org.objectweb.asm.tree.analysis.SourceValue
import java.net.URL
import java.net.URLClassLoader
import java.nio.file.FileSystems
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardOpenOption
import kotlin.streams.asSequence

/**
 * Repairs dex2jar mistranslations in every class of a dex2jar-produced extension jar (GAP-100).
 *
 * Three distinct dex2jar bugs are repaired:
 *  - **(c) object collapse** — `new X` mistranslated to `new java/lang/Object` with X's constructor
 *    dropped; undone by [repairObjectCollapse], a WHOLE-JAR pass that runs FIRST (the dropped constructor
 *    must be synthesized in a class OTHER than the one holding the `new`, so it cannot be done per class);
 *  - **(b) self-instantiation collapse** — a class instantiating itself (R8 lambda singletons, minified
 *    enum constants) mistranslated to `new <superclass>`; undone in [repairSelfInstantiation] before the
 *    frame recompute;
 *  - **(a) missing StackMapTable frames** — recomputed via `COMPUTE_FRAMES` (the original reason this
 *    exists; see below). It runs LAST on purpose: (b) and (c) rewrite instructions, so the frames must be
 *    recomputed from the CORRECTED instruction stream.
 *
 * ## Why this exists (bug (a))
 * Suwayomi's [suwayomi.tachidesk.manga.impl.util.PackageTools.dex2jar] converts an Android APK's
 * dex bytecode to a JVM jar via the `de.femtopedia.dex2jar` translator, then runs
 * `BytecodeEditor.fixAndroidClasses`, which rewrites each class with `ClassWriter(classReader, 0)`.
 * The `0` flags mean "do NOT recompute frames" and the passed-in `ClassReader` makes ASM copy each
 * method's original `StackMapTable` VERBATIM. For newer extension APKs (newer Kotlin/AGP/R8 output —
 * e.g. Asura Scans 1.6.66) the translator emits methods that BRANCH but whose `StackMapTable` is
 * incomplete (a frame is missing at a branch target). The class file is version 52 (Java 8), so the
 * JVM's strict type-checking verifier MANDATES a frame at every branch target and rejects the class
 * with:
 *
 *     java.lang.VerifyError: Expecting a stackmap frame at branch target N
 *
 * so the source cannot load. (Confirmed live against Asura Scans 1.6.66; the owner's only workaround
 * was to downgrade to 1.4.64. Downgrading the class version to 50 does NOT help — modern HotSpot uses
 * the strict verifier for v50 too and its fail-over to the legacy verifier does not rescue a
 * frame-less branch.)
 *
 * ## The fix
 * After dex2jar has produced the jar, re-serialize every class with `ClassWriter(COMPUTE_FRAMES)` and
 * — critically — WITHOUT handing the `ClassReader` to the writer, so ASM cannot re-use the original
 * (broken) frames and is forced to recompute a correct `StackMapTable` from the instruction stream.
 * This is idempotent on already-valid classes and independent of Suwayomi's `BytecodeEditor` pass (it
 * simply runs after it and corrects the frames that pass left broken), so we never fork upstream
 * Suwayomi — the Dockerfile keeps cloning it pristine at the pinned commit.
 *
 * ## Correctness of COMPUTE_FRAMES
 * Recomputing frames requires ASM to resolve the common superclass of two types at every control-flow
 * merge ([ClassWriter.getCommonSuperClass]), which loads the referenced classes. We point that
 * resolution at a loader spanning the extension jar itself PLUS the engine-host runtime classpath
 * (which carries the whole `eu.kanade.tachiyomi` extension-lib API, AndroidCompat's `android.*` shims,
 * okhttp, kotlin-stdlib, jsoup, kotlinx-serialization …) — i.e. every type a real extension can
 * reference — so the recomputed frames are correct, not widened to `Object`. Resolution uses
 * `Class.forName(name, initialize = false, …)`, which does not link/verify the sibling class, so
 * loading a not-yet-repaired class to answer a superclass query never re-triggers the very VerifyError
 * we are fixing.
 *
 * If a single class cannot be recomputed (e.g. it references a type genuinely absent from the
 * classpath, so `getCommonSuperClass` throws), that class is LEFT BYTE-FOR-BYTE UNCHANGED rather than
 * risk emitting a wrong frame — a deliberate fail-safe: the rewrite can only ever fix a class, never
 * turn a loadable one into a broken one. (The `Object`-widening fallback ASM documents is rejected on
 * purpose: it produces "Bad type on operand stack" frames when a real type can't be resolved.)
 */
object DexStackFrameRewriter {
    private val logger = KotlinLogging.logger {}

    /** The type dex2jar collapses a `new X` down to (bug (c)) — and the root of every class hierarchy. */
    private const val JAVA_LANG_OBJECT = "java/lang/Object"

    /**
     * Run the whole-jar object-collapse repair (bug (c)) and then recompute the StackMapTable of every
     * `.class` in [jarFile], resolving referenced types against the jar plus [referenceClassLoader] (the
     * engine-host runtime classpath). Classes that cannot be recomputed are kept unchanged. Best-effort
     * per class; a single failure never aborts the jar.
     *
     * The WHOLE pass is fail-safe too: a jar-level failure (opening/walking/closing the zip filesystem)
     * is logged and swallowed, leaving the jar exactly as dex2jar produced it. This repair can only ever
     * HELP — it must never be the reason [ExtensionLoader.loadFromApk] aborts, since without it that jar
     * would simply proceed to `loadExtensionSources` (as it did before this fix existed).
     */
    fun repairStackFrames(
        jarFile: Path,
        referenceClassLoader: ClassLoader,
    ) {
        // Bug (c) FIRST: it is whole-jar (it edits one class and synthesizes a constructor in another) and
        // it rewrites instructions, so it must land before the per-class COMPUTE_FRAMES walk recomputes
        // frames from the instruction stream. It guards itself, so it can never abort this pass.
        repairObjectCollapse(jarFile)
        try {
            val jarUrl = jarFile.toUri().toURL()
            // This resolver is PARENT-FIRST (a plain URLClassLoader), whereas the runtime loads the
            // extension CHILD-FIRST (ChildFirstURLClassLoader). For a type present in BOTH the jar and
            // the engine-host classpath they could pick different copies, so a computed common-supertype
            // could in theory diverge from the verifier's view. In practice it is a non-issue: an
            // extension's OWN classes are obfuscated and never collide with a parent type, and the
            // shared extension-lib/AndroidCompat/kotlin/okhttp types resolve to the SAME class either
            // way — proven by the live Asura 1.6.66 + 1.4.64 loads, where every recomputed frame the
            // verifier accepted. Left parent-first for simplicity; noted here in case a future collision
            // ever surfaces a "Bad type" that only a child-first resolver would explain.
            val typeResolver = URLClassLoader(arrayOf<URL>(jarUrl), referenceClassLoader)
            var repaired = 0
            FileSystems.newFileSystem(jarFile, null as ClassLoader?)?.use { fs ->
                Files
                    .walk(fs.getPath("/"))
                    .asSequence()
                    .filterNot(Files::isDirectory)
                    .filter { it.toString().endsWith(".class") }
                    .forEach { path ->
                        if (repairClass(path, typeResolver)) repaired++
                    }
            }
            logger.debug { "Stack-frame repair pass rewrote $repaired class(es) in ${jarFile.fileName}" }
        } catch (t: Throwable) {
            logger.warn(t) {
                "Stack-frame repair pass failed for ${jarFile.fileName}; leaving the jar as dex2jar " +
                    "produced it and continuing to load (repair is best-effort): ${t.message}"
            }
        }
    }

    /** Rewrite one class with recomputed frames; keep the original bytes on any failure. Returns true if changed. */
    private fun repairClass(
        path: Path,
        typeResolver: ClassLoader,
    ): Boolean {
        val original = Files.readAllBytes(path)
        if (!isClassFile(original)) return false
        val rewritten =
            try {
                recompute(original, typeResolver)
            } catch (t: Throwable) {
                // An unresolvable referenced type (or any ASM failure) -> keep the original class
                // untouched. Never emit a possibly-wrong frame; the rewrite must never make things worse.
                // WARN (not debug): if a future extension regresses here, this named trail is what turns a
                // bare load-time VerifyError into "left un-repaired: <class>: <reason>".
                logger.warn(t) { "Stack-frame recompute left ${path} un-repaired (kept original): ${t.message}" }
                return false
            }
        Files.write(path, rewritten, StandardOpenOption.CREATE, StandardOpenOption.TRUNCATE_EXISTING)
        return true
    }

    /**
     * Repair one class: (1) undo dex2jar's self-instantiation collapse (GAP-100 bug (b)), then
     * (2) recompute the StackMapTable (bug (a)). The class is read into an ASM tree so the instruction
     * edits and the COMPUTE_FRAMES write happen on one pass; the reader is NOT passed to the writer, so
     * the original (broken) frames are discarded and recomputed from the — now corrected — instructions.
     */
    private fun recompute(
        classBytes: ByteArray,
        typeResolver: ClassLoader,
    ): ByteArray {
        val node = ClassNode()
        ClassReader(classBytes).accept(node, ClassReader.EXPAND_FRAMES)
        repairSelfInstantiation(node)
        val writer = FrameComputingClassWriter(typeResolver)
        node.accept(writer)
        return writer.toByteArray()
    }

    /**
     * Undo dex2jar's SELF-INSTANTIATION collapse (GAP-100 bug (b)).
     *
     * ## The bug
     * When a class `X extends S` instantiates ITSELF — the R8 stateless-lambda singleton
     * (`class q extends kotlin.jvm.internal.Lambda { static final q a; static { a = new q(); } }`) and
     * every minified `enum` constant are the common cases — dex2jar 2.4.37 emits `new S` (the
     * SUPERCLASS) instead of `new X`, and drops X's own constructor. The result is bytecode that
     * `new`s an ABSTRACT base (`kotlin/jvm/internal/Lambda`, `java/lang/Enum`, …): the strict verifier
     * rejects it with `VerifyError: Bad type on operand stack` (the base is not assignable to the
     * field it is stored into), and with verification off it fails at runtime with
     * `InstantiationError`. Confirmed live on fmteam/dragontea (lambda) and anisa/madaradex (lambda +
     * enum); a version bump does not help (2.4.37 and 2.4.38 are byte-identical here).
     *
     * ## The repair — two retargeting gates by superclass
     * For each `new S` whose matching `invokespecial S.<init>(d)` we decide whether to retarget both to
     * `X` (and synthesize a forwarding constructor `X.<init>(d) { super(d); }`). Which gate applies is
     * decided by the superclass `S`:
     *  - **`S == java/lang/Enum` ⇒ retarget UNCONDITIONALLY.** A class extending abstract `java.lang.Enum`
     *    is definitionally an `enum`, and an enum only ever instantiates its OWN constants (it can never
     *    `new` a foreign enum), so EVERY `new java/lang/Enum` inside it is one of this enum's constants and
     *    is always self. We bypass the self-typed-sink gate here because R8 stores enum constants via a
     *    LOCAL first (`… ; astore N` … much later `aload N; putstatic`/`$VALUES aastore`), so the
     *    short-window [flowsIntoSelfTypedSink] scan MISSES them — retargeting only some of an enum's
     *    constants leaves a mix of `new X` / `new Enum` and re-breaks `<clinit>` with
     *    `VerifyError: Bad access to protected <init> method` (seen live on anisascans/madaradex).
     *  - **any other `S` (`kotlin/jvm/internal/Lambda`, …) ⇒ the self-typed-field gate.** Retarget only when
     *    the initialised object flows into a `put{static,field}` of a field TYPED `LX;` (or an enum
     *    `$VALUES` `AASTORE`) — see [flowsIntoSelfTypedSink]. This is conservative on purpose: a lambda `X`
     *    may legitimately create OTHER lambdas (dex2jar collapses those to `new S` too); those go into no
     *    `LX;` field and so are correctly SKIPPED. Only enums get the unconditional treatment because only
     *    enums extend `Enum` and an enum can never construct a foreign enum.
     * Purely local; frame correctness is restored by the subsequent COMPUTE_FRAMES pass. `S == null` (only
     * `java.lang.Object` has no super) short-circuits.
     */
    private fun repairSelfInstantiation(node: ClassNode) {
        val self = node.name
        val sup = node.superName ?: return
        val superIsEnum = sup == "java/lang/Enum"
        val synthesized = mutableSetOf<String>()

        for (method in node.methods) {
            val insns = method.instructions.toArray()
            for (i in insns.indices) {
                val alloc = insns[i]
                if (alloc !is TypeInsnNode || alloc.opcode != Opcodes.NEW || alloc.desc != sup) continue
                // Find the INDEX of the matching `invokespecial S.<init>` that initialises this allocation.
                val initIdx =
                    (i + 1 until insns.size)
                        .firstOrNull {
                            val n = insns[it]
                            n is MethodInsnNode && n.opcode == Opcodes.INVOKESPECIAL && n.owner == sup && n.name == "<init>"
                        } ?: continue
                val init = insns[initIdx] as MethodInsnNode
                // Enum: every `new Enum` here is one of this enum's own constants -> retarget unconditionally
                // (R8 stores constants via a local, so the window gate would miss them). Otherwise gate on the
                // self-typed sink so a lambda creating OTHER lambdas is never retargeted.
                if (!superIsEnum && !flowsIntoSelfTypedSink(insns, initIdx, self)) continue
                alloc.desc = self
                init.owner = self
                synthesized.add(init.desc)
            }
        }

        for (desc in synthesized) synthesizeForwardingCtor(node, desc)
    }

    /**
     * Give [node] the constructor `public <init>(desc) { super(<the same args>); }` that dex2jar dropped,
     * unless it already declares one with that exact [desc]. Returns true when a constructor was added.
     *
     * Shared by BOTH collapse repairs — bug (b) retargets a `new <superclass>` back to the class itself and
     * bug (c) retargets a `new java/lang/Object` back to the real type; either way the retargeted
     * `invokespecial <init>` now names a constructor that dex2jar never emitted, so one has to exist or the
     * class fails to link with `NoSuchMethodError`. Forwarding straight to the superclass constructor of the
     * same descriptor reproduces what the original constructor did for these (generated, field-free) shapes:
     * R8 lambda singletons, enum constants, and the small holder classes bug (c) hits.
     */
    private fun synthesizeForwardingCtor(
        node: ClassNode,
        desc: String,
    ): Boolean {
        val sup = node.superName ?: return false
        if (node.methods.any { it.name == "<init>" && it.desc == desc }) return false
        val ctor = MethodNode(Opcodes.ACC_PUBLIC, "<init>", desc, null, null)
        ctor.instructions.add(VarInsnNode(Opcodes.ALOAD, 0))
        var slot = 1
        for (arg in Type.getArgumentTypes(desc)) {
            ctor.instructions.add(VarInsnNode(arg.getOpcode(Opcodes.ILOAD), slot))
            slot += arg.size
        }
        ctor.instructions.add(MethodInsnNode(Opcodes.INVOKESPECIAL, sup, "<init>", desc, false))
        ctor.instructions.add(InsnNode(Opcodes.RETURN))
        node.methods.add(ctor)
        return true
    }

    /**
     * True when the object initialised at [initIdx] (an `invokespecial <init>`) is stored into a field
     * TYPED `L[self];` (a static or instance field of the class itself — the singleton/enum-constant
     * field) or an enum `$VALUES` array (`AASTORE`) within a few instructions. That destination proves
     * the allocated object was meant to be [self], not its superclass — the gate that keeps the repair
     * from ever touching a legitimate `new Superclass`.
     */
    private fun flowsIntoSelfTypedSink(
        insns: Array<org.objectweb.asm.tree.AbstractInsnNode>,
        initIdx: Int,
        self: String,
    ): Boolean {
        val window = (initIdx + 1 until minOf(insns.size, initIdx + 6))
        for (k in window) {
            val n = insns[k]
            if (n is FieldInsnNode &&
                (n.opcode == Opcodes.PUTSTATIC || n.opcode == Opcodes.PUTFIELD) &&
                n.owner == self &&
                n.desc == "L$self;"
            ) {
                return true
            }
            if (n is InsnNode && n.opcode == Opcodes.AASTORE) return true
        }
        return false
    }

    /** One constructor dex2jar dropped: the class that must declare it, and the descriptor it must have. */
    private data class MissingCtor(
        val owner: String,
        val desc: String,
    )

    /**
     * Undo dex2jar's OBJECT collapse across the WHOLE jar (GAP-100 bug (c)).
     *
     * ## The bug
     * Alongside the self-instantiation collapse (bug (b)), dex2jar also mistranslates `new X` to
     * `new java/lang/Object` for unrelated classes X and drops X's constructor. Observed live on
     * anisa/fmteam, where an interceptor allocates a tiny flag holder:
     *
     *     15: new java/lang/Object ; dup ; invokespecial java/lang/Object.<init>()V ; astore_3
     *     23: aload_3 ; 25: putfield b0.a:Z          <-- the objectref MUST be a b0
     *
     * while `b0` is `public final class b0 { public boolean a; }` with NO constructor at all. The strict
     * verifier rejects the loading class with
     * `VerifyError: Bad type on operand stack in putfield — Type 'java/lang/Object' is not assignable to 'b0'`,
     * so the whole source fails to load.
     *
     * ## The recovery — the intended type comes from USAGE
     * The lost type is still implied by what the code DOES with the value: a receiver (objectref) is only
     * legal if its type is assignable to the member's owner. So whenever a freshly-`new Object`ed value is
     * the receiver of `putfield` / `getfield` / `invokevirtual` / `invokeinterface` / `invokespecial`
     * (non-`<init>`) whose owner is a concrete class X, that value must have been an X. The receiver is
     * located with `Analyzer(SourceInterpreter())`, which gives, for every instruction, the set of
     * instructions that produced each operand-stack value.
     *
     * This is a WHOLE-JAR pass rather than a per-class one because the two halves of the repair live in
     * DIFFERENT classes: the `new` is retargeted in the class that allocates, while the dropped constructor
     * has to be synthesized on X.
     *
     * ## The safety gates (a wrong retarget corrupts a WORKING extension — this runs on every load)
     *  1. **Usage-driven only** — a `new Object` with no concrete-class receiver-use is never touched, so a
     *     genuine `new Object()` (a lock/monitor, an `Object`-typed field, a sentinel) is left exactly as-is.
     *  2. **In-jar owners only** — X must be a class DEFINED IN THIS JAR. We never retarget to a
     *     library/framework type: we could not synthesize its constructor, and a receiver-use of a
     *     framework type on a genuine `Object` is far likelier to be our own misreading than a dex2jar bug.
     *  3. **Instantiable only** — X must not be an interface or abstract (`new` on either is illegal), which
     *     also makes `invokeinterface` receivers a no-op, and never `java/lang/Object` itself or an array.
     *  4. **Unambiguous owner** — if the same `new` reaches receiver-uses of two different owners we cannot
     *     tell which was intended, so it is skipped entirely rather than guessed.
     *  5. **Unambiguous initializer** — the `invokespecial java/lang/Object.<init>` that initialises the
     *     allocation is identified through the SAME dataflow (not by textual proximity); if it is missing or
     *     several are found, nothing is rewritten — a `new X` left initialised by `Object.<init>` would be
     *     worse than the original defect.
     *  6. **Fail-safe, at three levels** — a method whose dataflow cannot be analysed is skipped; classes are
     *     only re-serialized AFTER every class has been analysed AND only when this pass actually changed
     *     them (an untouched class keeps its original bytes byte-for-byte); and any throwable at all leaves
     *     the jar exactly as dex2jar produced it and only logs a WARN. Like the frame pass, this repair must
     *     never be the reason a load aborts.
     *
     * Classes are read with `SKIP_FRAMES` and written back with `COMPUTE_MAXS` only: the retarget invalidates
     * the original StackMapTable, and the per-class [recompute] walk that runs immediately afterwards
     * recomputes it from the corrected instructions — computing frames here would just be thrown away.
     */
    private fun repairObjectCollapse(jarFile: Path) {
        try {
            FileSystems.newFileSystem(jarFile, null as ClassLoader?)?.use { fs ->
                val classes = LinkedHashMap<Path, ClassNode>()
                Files
                    .walk(fs.getPath("/"))
                    .asSequence()
                    .filterNot(Files::isDirectory)
                    .filter { it.toString().endsWith(".class") }
                    .forEach { path ->
                        val bytes = Files.readAllBytes(path)
                        if (!isClassFile(bytes)) return@forEach
                        val node = ClassNode()
                        ClassReader(bytes).accept(node, ClassReader.SKIP_FRAMES)
                        classes[path] = node
                    }

                val inJar = classes.values.associateBy(ClassNode::name)
                val missingCtors = LinkedHashSet<MissingCtor>()
                val changed = mutableSetOf<String>()
                for (node in classes.values) {
                    if (retargetCollapsedAllocations(node, inJar, missingCtors)) changed += node.name
                }
                for (ctor in missingCtors) {
                    val target = inJar[ctor.owner] ?: continue
                    if (synthesizeForwardingCtor(target, ctor.desc)) changed += target.name
                }
                if (changed.isEmpty()) return@use

                // Serialize everything BEFORE touching the jar, so an ASM failure on the last class cannot
                // leave the jar half-rewritten (gate 6).
                val rewritten =
                    classes
                        .filterValues { it.name in changed }
                        .mapValues { (_, node) ->
                            val writer = ClassWriter(ClassWriter.COMPUTE_MAXS)
                            node.accept(writer)
                            writer.toByteArray()
                        }
                for ((path, bytes) in rewritten) {
                    Files.write(path, bytes, StandardOpenOption.CREATE, StandardOpenOption.TRUNCATE_EXISTING)
                }
                logger.debug { "Object-collapse repair rewrote ${rewritten.size} class(es) in ${jarFile.fileName}" }
            }
        } catch (t: Throwable) {
            logger.warn(t) {
                "Object-collapse repair failed for ${jarFile.fileName}; leaving the jar as dex2jar produced " +
                    "it and continuing to load (repair is best-effort): ${t.message}"
            }
        }
    }

    /**
     * Retarget every collapsed `new java/lang/Object` in [node] whose intended type is recoverable from
     * usage, recording each constructor that must then be synthesized into [missingCtors]. [inJar] is every
     * class this jar defines, keyed by internal name — the whitelist of types we are willing to retarget to
     * (gate 2). Returns true when [node] was modified.
     */
    private fun retargetCollapsedAllocations(
        node: ClassNode,
        inJar: Map<String, ClassNode>,
        missingCtors: MutableSet<MissingCtor>,
    ): Boolean {
        var changed = false
        for (method in node.methods) {
            val insns = method.instructions.toArray()
            // Cheap pre-filter: no collapsed allocation in this method means no dataflow analysis at all.
            if (insns.none { it is TypeInsnNode && it.opcode == Opcodes.NEW && it.desc == JAVA_LANG_OBJECT }) continue
            val candidates =
                try {
                    collectCollapseCandidates(node.name, method)
                } catch (t: Throwable) {
                    // A method dex2jar left un-analysable is skipped, not fatal (gate 6): the rest of the jar
                    // still gets repaired, and this method is simply left as dex2jar produced it.
                    logger.debug(t) { "Object-collapse analysis skipped ${node.name}.${method.name}: ${t.message}" }
                    continue
                }
            for ((alloc, owners) in candidates.owners) {
                val owner = owners.singleOrNull() ?: continue // gate 4: ambiguous -> never guess
                val target = inJar[owner] ?: continue // gate 2: in-jar types only
                if (!isInstantiable(target)) continue // gate 3
                val init = candidates.initializers[alloc]?.singleOrNull() ?: continue // gate 5
                alloc.desc = owner
                init.owner = owner
                missingCtors += MissingCtor(owner, init.desc)
                changed = true
            }
        }
        return changed
    }

    /** True when `new X` on [node] is legal at all — i.e. X is neither an interface nor abstract (gate 3). */
    private fun isInstantiable(node: ClassNode): Boolean =
        node.access and (Opcodes.ACC_INTERFACE or Opcodes.ACC_ABSTRACT) == 0

    /**
     * What the dataflow analysis recovered for ONE method: per collapsed `new java/lang/Object`, the owners
     * of the members it is used as a receiver of, and the `invokespecial java/lang/Object.<init>` calls that
     * initialise it. Sets (not single values) on purpose — the ambiguity gates are "exactly one".
     */
    private class CollapseCandidates {
        val owners = LinkedHashMap<TypeInsnNode, MutableSet<String>>()
        val initializers = LinkedHashMap<TypeInsnNode, MutableSet<MethodInsnNode>>()
    }

    /**
     * Run `Analyzer(SourceInterpreter())` over [method] of class [ownerName] and, for every instruction that
     * consumes an object RECEIVER, attribute that receiver back to the `new java/lang/Object` that produced
     * it. Throws [org.objectweb.asm.tree.analysis.AnalyzerException] on bytecode it cannot model — the
     * caller treats that as "skip this method".
     */
    private fun collectCollapseCandidates(
        ownerName: String,
        method: MethodNode,
    ): CollapseCandidates {
        val frames = Analyzer(SourceInterpreter()).analyze(ownerName, method)
        val insns = method.instructions.toArray()
        val found = CollapseCandidates()
        for (i in insns.indices) {
            val insn = insns[i]
            val frame = frames[i] ?: continue // unreachable (dead) code carries no frame
            val receiverIndex = receiverStackIndex(insn, frame.stackSize) ?: continue
            if (receiverIndex < 0) continue
            val allocations = resolveObjectAllocations(frame.getStack(receiverIndex), frames, method)
            if (allocations.isEmpty()) continue
            if (insn is MethodInsnNode && insn.name == "<init>") {
                if (insn.owner != JAVA_LANG_OBJECT) continue
                for (alloc in allocations) found.initializers.getOrPut(alloc) { mutableSetOf() } += insn
            } else {
                val owner = receiverOwner(insn) ?: continue
                for (alloc in allocations) found.owners.getOrPut(alloc) { mutableSetOf() } += owner
            }
        }
        return found
    }

    /**
     * Where the object RECEIVER of [insn] sits in an operand stack of [stackSize] values, or null when
     * [insn] consumes no receiver.
     *
     * Indices count VALUES, not JVM slots: ASM's analysis [Frame] pushes one entry per value, so a `long`
     * argument occupies a single stack entry (of size 2). Sizing this by slots would mis-locate the receiver
     * of any `putfield` of a `long`/`double` field or of any call taking one.
     */
    private fun receiverStackIndex(
        insn: AbstractInsnNode,
        stackSize: Int,
    ): Int? =
        when (insn.opcode) {
            Opcodes.GETFIELD -> stackSize - 1
            Opcodes.PUTFIELD -> stackSize - 2 // objectref, value
            Opcodes.INVOKEVIRTUAL, Opcodes.INVOKEINTERFACE, Opcodes.INVOKESPECIAL ->
                stackSize - Type.getArgumentTypes((insn as MethodInsnNode).desc).size - 1
            else -> null
        }

    /**
     * The class that the member accessed by [insn] belongs to — the type its receiver must be assignable to,
     * i.e. the type we recover. Null when it tells us nothing usable: `java/lang/Object` itself (every value
     * is already assignable to it) or an array type (`[I.clone()`), neither of which can ever be the
     * collapsed type.
     */
    private fun receiverOwner(insn: AbstractInsnNode): String? {
        val owner =
            when (insn) {
                is FieldInsnNode -> insn.owner
                is MethodInsnNode -> insn.owner
                else -> return null
            }
        return owner.takeUnless { it == JAVA_LANG_OBJECT || it.startsWith("[") }
    }

    /**
     * Walk [value]'s producing instructions back to the `new java/lang/Object` allocations it can hold.
     *
     * A backwards walk is required because `SourceInterpreter` re-bases a value's source on every COPY:
     * after `new Object ; dup ; astore_3 … aload_3`, the receiver's recorded producer is the `aload`, not
     * the `new`. So copy instructions (`aload` / `astore` / `dup` / `checkcast`) are followed to the value
     * they copied, until an allocation is reached. Any other producer (a method return, a field read, a
     * merge of an untracked value) ends that branch of the walk: unknown provenance simply yields no
     * allocation, which means no retarget.
     */
    private fun resolveObjectAllocations(
        value: SourceValue,
        frames: Array<Frame<SourceValue>?>,
        method: MethodNode,
    ): Set<TypeInsnNode> {
        val allocations = LinkedHashSet<TypeInsnNode>()
        val visited = mutableSetOf<AbstractInsnNode>()
        val pending = ArrayDeque(value.insns)
        while (pending.isNotEmpty()) {
            val producer = pending.removeFirst()
            if (!visited.add(producer)) continue // a value copied around a loop must not spin forever
            if (producer is TypeInsnNode && producer.opcode == Opcodes.NEW) {
                if (producer.desc == JAVA_LANG_OBJECT) allocations += producer
                continue
            }
            val frame = frames.getOrNull(method.instructions.indexOf(producer)) ?: continue
            val copied =
                when (producer.opcode) {
                    Opcodes.ALOAD -> frame.getLocal((producer as VarInsnNode).`var`)
                    Opcodes.ASTORE, Opcodes.DUP, Opcodes.CHECKCAST ->
                        if (frame.stackSize > 0) frame.getStack(frame.stackSize - 1) else null
                    else -> null // deliberately conservative: DUP_X1/SWAP/… end the walk instead of guessing
                } ?: continue
            pending += copied.insns
        }
        return allocations
    }

    /** A class file starts with the 0xCAFEBABE magic; anything else in the jar is skipped. */
    private fun isClassFile(bytes: ByteArray): Boolean =
        bytes.size >= 4 &&
            bytes[0] == 0xCA.toByte() &&
            bytes[1] == 0xFE.toByte() &&
            bytes[2] == 0xBA.toByte() &&
            bytes[3] == 0xBE.toByte()

    /**
     * [ClassWriter] with `COMPUTE_FRAMES` whose superclass resolution uses [loader] (the extension jar
     * + engine-host runtime classpath) instead of the default (this class's own loader, which can't see
     * the extension's classes). No `Object` fallback: if a type is unresolvable the default
     * `getCommonSuperClass` throws, and [repairClass] catches it and keeps the original class.
     */
    private class FrameComputingClassWriter(
        private val loader: ClassLoader,
    ) : ClassWriter(COMPUTE_FRAMES) {
        override fun getClassLoader(): ClassLoader = loader
    }
}
