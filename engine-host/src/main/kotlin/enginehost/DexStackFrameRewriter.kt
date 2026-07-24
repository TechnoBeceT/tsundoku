package enginehost

import io.github.oshai.kotlinlogging.KotlinLogging
import org.objectweb.asm.ClassReader
import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Opcodes
import org.objectweb.asm.Type
import org.objectweb.asm.tree.ClassNode
import org.objectweb.asm.tree.FieldInsnNode
import org.objectweb.asm.tree.InsnNode
import org.objectweb.asm.tree.MethodInsnNode
import org.objectweb.asm.tree.MethodNode
import org.objectweb.asm.tree.TypeInsnNode
import org.objectweb.asm.tree.VarInsnNode
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
 * Two distinct dex2jar bugs are repaired in one pass, per class:
 *  - **(a) missing StackMapTable frames** — recomputed via `COMPUTE_FRAMES` (the original reason this
 *    exists; see below);
 *  - **(b) self-instantiation collapse** — a class instantiating itself (R8 lambda singletons, minified
 *    enum constants) mistranslated to `new <superclass>`; undone in [repairSelfInstantiation] before the
 *    frame recompute.
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

    /**
     * Recompute the StackMapTable of every `.class` in [jarFile], resolving referenced types against
     * the jar plus [referenceClassLoader] (the engine-host runtime classpath). Classes that cannot be
     * recomputed are kept unchanged. Best-effort per class; a single failure never aborts the jar.
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

        for (desc in synthesized) {
            if (node.methods.any { it.name == "<init>" && it.desc == desc }) continue
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
        }
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
