package enginehost

/*
 * Pins the engine-host extension-update VerifyError (GAP-100, branch v2). Newer extension APKs (newer
 * Kotlin/AGP/R8 output — e.g. Asura Scans 1.6.66) make Suwayomi's dex2jar path emit classes that
 * BRANCH but whose StackMapTable is missing a frame at the branch target. The class is version 52
 * (Java 8), so the JVM's strict verifier rejects it with:
 *
 *     java.lang.VerifyError: Expecting a stackmap frame at branch target N
 *
 * and the source cannot load (the owner's only workaround was downgrading to 1.4.64). The tests below
 * reproduce that exact defect with a synthetic, dependency-free class built by ASM — a branch with no
 * StackMapTable at class version 52 — so the proof commits no binary APK fixture and cannot rot:
 *  1) the raw class fails verification with the exact production error, and
 *  2) after DexStackFrameRewriter recomputes its frames it loads AND runs correctly, while a class
 *     that was already valid is left working (no regression on older, well-formed extensions).
 */

import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Label
import org.objectweb.asm.MethodVisitor
import org.objectweb.asm.Opcodes
import java.nio.file.Files
import java.nio.file.Path
import java.util.jar.JarEntry
import java.util.jar.JarOutputStream
import kotlin.io.path.createTempDirectory
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class DexStackFrameRewriterTest {
    private val tmp: Path = createTempDirectory("gap100")

    @AfterTest
    fun cleanup() {
        tmp.toFile().deleteRecursively()
    }

    /** Defines classes directly from bytes so we can force the JVM to link (and thus verify) them. */
    private class BytesLoader : ClassLoader(BytesLoader::class.java.classLoader) {
        fun define(
            name: String,
            bytes: ByteArray,
        ): Class<*> = defineClass(name, bytes, 0, bytes.size)
    }

    /**
     * A class version 52 (Java 8) method with a conditional branch and NO StackMapTable — exactly the
     * shape dex2jar mis-emits. `withFrames=true` asks ASM to compute correct frames (a well-formed
     * class, modelling an older loadable extension).
     */
    private fun pickerClass(
        internalName: String,
        withFrames: Boolean,
    ): ByteArray {
        val flags = if (withFrames) ClassWriter.COMPUTE_FRAMES else 0
        val cw = ClassWriter(flags)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv: MethodVisitor =
            cw.visitMethod(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "pick", "(I)Ljava/lang/String;", null, null)
        mv.visitCode()
        val elseLabel = Label()
        mv.visitVarInsn(Opcodes.ILOAD, 0)
        mv.visitJumpInsn(Opcodes.IFEQ, elseLabel) // branch whose target needs a stackmap frame
        mv.visitLdcInsn("nonzero")
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitLabel(elseLabel) // <- strict verifier demands a frame here
        mv.visitLdcInsn("zero")
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitMaxs(1, 1)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A class version 52 method with NO StackMapTable whose two if/else branches assign DISTINCT
     * reference types — `java.util.ArrayList` and `java.util.LinkedList` — to the SAME local, so at the
     * post-branch merge point COMPUTE_FRAMES cannot short-circuit: it MUST call `getCommonSuperClass`
     * and resolve their common superclass (`java.util.AbstractList`) against the reference classpath.
     * The method's declared return type is `AbstractList`, so if the merge resolved wrong (e.g. widened
     * to `Object`) the class would fail verification with "Bad type on operand stack" — the frame is
     * only accepted if the loader genuinely resolved the hierarchy. This exercises the fragile half of
     * the rewriter (type resolution), which the frame-DISCARD tests never touch.
     */
    private fun mergeClass(internalName: String): ByteArray {
        val cw = ClassWriter(0) // no frames, no reader -> a frameless branch target at the merge
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv =
            cw.visitMethod(
                Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC,
                "pick",
                "(Z)Ljava/util/AbstractList;",
                null,
                null,
            )
        mv.visitCode()
        val elseLabel = Label()
        val endLabel = Label()
        mv.visitVarInsn(Opcodes.ILOAD, 0)
        mv.visitJumpInsn(Opcodes.IFEQ, elseLabel)
        // then: local 1 = new ArrayList()
        mv.visitTypeInsn(Opcodes.NEW, "java/util/ArrayList")
        mv.visitInsn(Opcodes.DUP)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/util/ArrayList", "<init>", "()V", false)
        mv.visitVarInsn(Opcodes.ASTORE, 1)
        mv.visitJumpInsn(Opcodes.GOTO, endLabel)
        // else: local 1 = new LinkedList()
        mv.visitLabel(elseLabel)
        mv.visitTypeInsn(Opcodes.NEW, "java/util/LinkedList")
        mv.visitInsn(Opcodes.DUP)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/util/LinkedList", "<init>", "()V", false)
        mv.visitVarInsn(Opcodes.ASTORE, 1)
        // merge: local 1 is ArrayList (then) merged with LinkedList (else) => AbstractList
        mv.visitLabel(endLabel)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitMaxs(2, 2)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** Writes a single class into a fresh jar under [tmp] and returns its path. */
    private fun jarWith(
        internalName: String,
        bytes: ByteArray,
    ): Path {
        val jar = tmp.resolve("${internalName.substringAfterLast('/')}.jar")
        JarOutputStream(Files.newOutputStream(jar)).use { out ->
            out.putNextEntry(JarEntry("$internalName.class"))
            out.write(bytes)
            out.closeEntry()
        }
        return jar
    }

    private fun classBytesFromJar(
        jar: Path,
        internalName: String,
    ): ByteArray =
        java.util.zip.ZipFile(jar.toFile()).use { zf ->
            zf.getInputStream(zf.getEntry("$internalName.class")).readAllBytes()
        }

    @Test
    fun `raw dex2jar-style class with a frameless branch fails JVM verification`() {
        val broken = pickerClass("Broken", withFrames = false)
        // Linking (which getDeclaredMethod forces) runs the bytecode verifier.
        val error =
            assertFailsWith<VerifyError> {
                BytesLoader().define("Broken", broken).getDeclaredMethod("pick", Int::class.javaPrimitiveType)
            }
        assertTrue(
            error.message!!.contains("stackmap frame"),
            "expected the production 'Expecting a stackmap frame at branch target' VerifyError, got: ${error.message}",
        )
    }

    @Test
    fun `repairStackFrames makes the broken class verify and run correctly`() {
        val jar = jarWith("Broken", pickerClass("Broken", withFrames = false))

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        val repaired = classBytesFromJar(jar, "Broken")
        val method = BytesLoader().define("Broken", repaired).getDeclaredMethod("pick", Int::class.javaPrimitiveType)
        assertEquals("nonzero", method.invoke(null, 5), "repaired class must verify, link and run")
        assertEquals("zero", method.invoke(null, 0))
    }

    @Test
    fun `repairStackFrames leaves an already-valid class loadable (no regression)`() {
        // An older, well-formed extension (frames already present and correct) must still load.
        val jar = jarWith("Fine", pickerClass("Fine", withFrames = true))

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        val out = classBytesFromJar(jar, "Fine")
        val method = BytesLoader().define("Fine", out).getDeclaredMethod("pick", Int::class.javaPrimitiveType)
        assertEquals("nonzero", method.invoke(null, 1))
    }

    @Test
    fun `repairStackFrames computes a real supertype merge against the reference classpath`() {
        // Forces getCommonSuperClass(ArrayList, LinkedList) -> AbstractList: the type-resolution half of
        // the rewriter, which the pure frame-DISCARD tests never exercise. The declared return type is
        // AbstractList, so the class only verifies if that merge resolved correctly (a wrong merge to
        // Object would fail "Bad type on operand stack").
        val jar = jarWith("Merge", mergeClass("Merge"))

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        val repaired = classBytesFromJar(jar, "Merge")
        val method = BytesLoader().define("Merge", repaired).getDeclaredMethod("pick", Boolean::class.javaPrimitiveType)
        // Verified + linked + run: the merge frame was computed via the reference-classpath loader.
        assertTrue(method.invoke(null, true) is java.util.ArrayList<*>, "then-branch returns the ArrayList")
        assertTrue(method.invoke(null, false) is java.util.LinkedList<*>, "else-branch returns the LinkedList")
    }
}
