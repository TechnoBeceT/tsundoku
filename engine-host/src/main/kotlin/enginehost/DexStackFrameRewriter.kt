package enginehost

import io.github.oshai.kotlinlogging.KotlinLogging
import org.objectweb.asm.ClassReader
import org.objectweb.asm.ClassVisitor
import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Opcodes
import java.net.URL
import java.net.URLClassLoader
import java.nio.file.FileSystems
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardOpenOption
import kotlin.streams.asSequence

/**
 * Repairs the StackMapTable of every class in a dex2jar-produced extension jar (GAP-100).
 *
 * ## Why this exists
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

    /** COMPUTE_FRAMES rewrite. Reader is NOT passed to the writer, so original frames are discarded. */
    private fun recompute(
        classBytes: ByteArray,
        typeResolver: ClassLoader,
    ): ByteArray {
        val reader = ClassReader(classBytes)
        val writer = FrameComputingClassWriter(typeResolver)
        reader.accept(object : ClassVisitor(Opcodes.ASM9, writer) {}, ClassReader.EXPAND_FRAMES)
        return writer.toByteArray()
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
