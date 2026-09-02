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
 *
 * The same synthetic-class approach pins the two TYPE-collapse defects the rewriter also repairs: the
 * self-instantiation collapse (`new <superclass>`, bug (b)) and the object collapse (`new
 * java/lang/Object` with the real type's constructor dropped, bug (c)). For bug (c) the proof runs both
 * ways — the collapsed allocation IS retargeted from its usage, and a genuine `new Object()` (a lock)
 * plus an allocation whose intended type is ambiguous are BOTH left untouched, because a wrong retarget
 * would corrupt an extension that works today.
 */

import org.objectweb.asm.ClassReader
import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Label
import org.objectweb.asm.MethodVisitor
import org.objectweb.asm.Opcodes
import org.objectweb.asm.tree.ClassNode
import org.objectweb.asm.tree.MethodInsnNode
import org.objectweb.asm.tree.TypeInsnNode
import java.nio.file.Files
import java.nio.file.Path
import java.util.jar.JarEntry
import java.util.jar.JarOutputStream
import kotlin.io.path.createTempDirectory
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertContentEquals
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

    /** An abstract superclass with a no-arg constructor — models `kotlin.jvm.internal.Lambda` / `java.lang.Enum`. */
    private fun abstractBase(internalName: String): ByteArray {
        val cw = ClassWriter(ClassWriter.COMPUTE_FRAMES)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC or Opcodes.ACC_ABSTRACT, internalName, null, "java/lang/Object", null)
        val mv = cw.visitMethod(Opcodes.ACC_PROTECTED, "<init>", "()V", null, null)
        mv.visitCode()
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false)
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(1, 1)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A class version 52 singleton `X extends [base]` whose `<clinit>` was mistranslated exactly as
     * dex2jar mis-emits the R8 stateless-lambda / enum-constant self-instantiation: it does `new [base]`
     * (the ABSTRACT superclass) instead of `new X`, stores it into the X-typed field `a`, and X has NO
     * constructor of its own. Raw, this fails verification ("Bad type on operand stack": base is not
     * assignable to the `LX;` field); after the rewriter's self-instantiation repair it must load and
     * `X.a` must hold a real X.
     */
    private fun collapsedSingleton(
        self: String,
        base: String,
    ): ByteArray {
        val cw = ClassWriter(0) // no frames, no synthesized ctor — exactly dex2jar's broken output
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC or Opcodes.ACC_FINAL, self, null, base, null)
        cw.visitField(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC or Opcodes.ACC_FINAL, "a", "L$self;", null, null).visitEnd()
        val mv = cw.visitMethod(Opcodes.ACC_STATIC, "<clinit>", "()V", null, null)
        mv.visitCode()
        mv.visitTypeInsn(Opcodes.NEW, base) // BUG: dex2jar emits the superclass instead of `self`
        mv.visitInsn(Opcodes.DUP)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, base, "<init>", "()V", false) // BUG: base ctor
        mv.visitFieldInsn(Opcodes.PUTSTATIC, self, "a", "L$self;") // destination proves it should be `self`
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(2, 0)
        mv.visitEnd()
        // deliberately NO `<init>` — dex2jar dropped it
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A class version 52 `enum X extends java/lang/Enum` whose `<clinit>` was mistranslated exactly as
     * dex2jar mis-emits R8's enum-constant self-instantiation, in the shape that DEFEATS the short-window
     * self-typed-sink scan: it creates BOTH constants first (`new java/lang/Enum … astore`), storing each
     * into a LOCAL, and only THEN writes them out (`aload; putstatic X.A:LX;`). So the `putstatic` that
     * proves constant `A` is self sits ~8 instructions after A's `invokespecial`, outside the window — a
     * window gate would retarget only `B` (whose store is near its init) and leave `A` as `new Enum`,
     * re-breaking `<clinit>`. Raw, it fails verification (`Bad access to protected <init> method`: a bare
     * `java.lang.Enum` cannot invoke its protected constructor from `X`); after the rewriter's
     * enum-unconditional repair BOTH constants must become real `X` instances.
     */
    private fun collapsedEnumViaLocals(self: String): ByteArray {
        val cw = ClassWriter(0) // no frames, no synthesized ctor — exactly dex2jar's broken output
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC or Opcodes.ACC_FINAL or Opcodes.ACC_ENUM, self, null, "java/lang/Enum", null)
        val fieldFlags = Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC or Opcodes.ACC_FINAL or Opcodes.ACC_ENUM
        cw.visitField(fieldFlags, "A", "L$self;", null, null).visitEnd()
        cw.visitField(fieldFlags, "B", "L$self;", null, null).visitEnd()
        val mv = cw.visitMethod(Opcodes.ACC_STATIC, "<clinit>", "()V", null, null)
        mv.visitCode()
        // Constant A: new Enum(name="A", ordinal=0) -> local 0 (BUG: dex2jar `new`s the Enum superclass)
        mv.visitTypeInsn(Opcodes.NEW, "java/lang/Enum")
        mv.visitInsn(Opcodes.DUP)
        mv.visitLdcInsn("A")
        mv.visitInsn(Opcodes.ICONST_0)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Enum", "<init>", "(Ljava/lang/String;I)V", false)
        mv.visitVarInsn(Opcodes.ASTORE, 0)
        // Constant B: new Enum(name="B", ordinal=1) -> local 1
        mv.visitTypeInsn(Opcodes.NEW, "java/lang/Enum")
        mv.visitInsn(Opcodes.DUP)
        mv.visitLdcInsn("B")
        mv.visitInsn(Opcodes.ICONST_1)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Enum", "<init>", "(Ljava/lang/String;I)V", false)
        mv.visitVarInsn(Opcodes.ASTORE, 1)
        // Only NOW write them out — A's putstatic is far past A's `invokespecial`, so the window gate misses it.
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitFieldInsn(Opcodes.PUTSTATIC, self, "A", "L$self;")
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitFieldInsn(Opcodes.PUTSTATIC, self, "B", "L$self;")
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(4, 2)
        mv.visitEnd()
        // deliberately NO `<init>` — dex2jar dropped it
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** Writes several classes into one jar under [tmp]. */
    private fun jarWithClasses(vararg classes: Pair<String, ByteArray>): Path {
        val jar = tmp.resolve("multi-${classes.first().first.substringAfterLast('/')}.jar")
        JarOutputStream(Files.newOutputStream(jar)).use { out ->
            for ((name, bytes) in classes) {
                out.putNextEntry(JarEntry("$name.class"))
                out.write(bytes)
                out.closeEntry()
            }
        }
        return jar
    }

    @Test
    fun `raw dex2jar self-instantiation collapse fails JVM verification`() {
        val loader = BytesLoader()
        loader.define("SelfBase", abstractBase("SelfBase"))
        // Linking the singleton verifies its <clinit>, which stores a `new SelfBase` into an LSelf; field.
        assertFailsWith<VerifyError> {
            loader.define("SelfSingle", collapsedSingleton("SelfSingle", "SelfBase")).getDeclaredFields()
        }
    }

    @Test
    fun `repairStackFrames undoes the self-instantiation collapse and the singleton initialises`() {
        val jar =
            jarWithClasses(
                "FixBase" to abstractBase("FixBase"),
                "FixSingle" to collapsedSingleton("FixSingle", "FixBase"),
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        val loader = BytesLoader()
        loader.define("FixBase", classBytesFromJar(jar, "FixBase"))
        val single = loader.define("FixSingle", classBytesFromJar(jar, "FixSingle"))
        // Class.forName(initialize = true) runs the repaired <clinit>; the singleton field must hold a
        // real FixSingle (not the abstract base, and not a verification failure).
        val initialised = Class.forName("FixSingle", true, loader)
        val instance = initialised.getField("a").get(null)
        assertTrue(instance != null, "the singleton field must be initialised after the repair")
        assertEquals("FixSingle", instance.javaClass.name, "the singleton must be an instance of the class itself, not its abstract superclass")
        assertTrue(single.superclass.name == "FixBase", "sanity: the repaired class still extends its real superclass")
    }

    @Test
    fun `raw dex2jar enum-via-local self-instantiation collapse fails JVM verification`() {
        // Linking the enum verifies its <clinit>, which `new`s the abstract java.lang.Enum and invokes its
        // protected constructor from a foreign type -> "Bad access to protected <init> method".
        assertFailsWith<VerifyError> {
            BytesLoader().define("RawEnum", collapsedEnumViaLocals("RawEnum")).getDeclaredFields()
        }
    }

    @Test
    fun `repairStackFrames retargets EVERY enum constant even when the window gate would miss it`() {
        // The window gate catches B (its putstatic is near its init) but MISSES A (created into a local,
        // stored much later). The old behaviour would retarget only B and leave `new Enum` for A, so the
        // repaired <clinit> would still fail verification. The enum-unconditional gate retargets both.
        val jar = jarWith("FixEnum", collapsedEnumViaLocals("FixEnum"))

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        val loader = BytesLoader()
        loader.define("FixEnum", classBytesFromJar(jar, "FixEnum"))
        // Class.forName(initialize = true) runs the repaired <clinit>; both constants must be real FixEnums.
        val initialised = Class.forName("FixEnum", true, loader)
        for (name in listOf("A", "B")) {
            val constant = initialised.getField(name).get(null)
            assertTrue(constant != null, "enum constant $name must be initialised after the repair")
            assertEquals(
                "FixEnum",
                constant.javaClass.name,
                "enum constant $name must be an instance of the enum itself, not its abstract java.lang.Enum superclass",
            )
        }
    }

    /**
     * A holder exactly like the live anisa/fmteam `b0`: `public final class X { public boolean f; }` with NO
     * constructor at all, because dex2jar dropped it along with the `new X` that created it.
     */
    private fun fieldHolder(
        internalName: String,
        fieldName: String,
    ): ByteArray {
        val cw = ClassWriter(ClassWriter.COMPUTE_FRAMES)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC or Opcodes.ACC_FINAL, internalName, null, "java/lang/Object", null)
        cw.visitField(Opcodes.ACC_PUBLIC, fieldName, "Z", null, null).visitEnd()
        // deliberately NO `<init>` — dex2jar dropped it
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A class version 52 user of [holder] mistranslated exactly as dex2jar mis-emits the OBJECT collapse: it
     * allocates a bare `new java/lang/Object`, parks it in a local, and then uses that local as the RECEIVER
     * of `putfield`/`getfield` on [holder] — so the value it created can only ever have been a [holder].
     * Raw, the strict verifier rejects it ("Type 'java/lang/Object' is not assignable to '[holder]'"); after
     * the repair `run()` must return the boolean it round-trips through the holder's field.
     */
    private fun collapsedHolderUser(
        self: String,
        holder: String,
        fieldName: String,
    ): ByteArray {
        val cw = ClassWriter(0) // no frames — exactly dex2jar's broken output
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, self, null, "java/lang/Object", null)
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "run", "()Z", null, null)
        mv.visitCode()
        mv.visitTypeInsn(Opcodes.NEW, "java/lang/Object") // BUG: dex2jar collapsed `new [holder]`
        mv.visitInsn(Opcodes.DUP)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false) // BUG: Object ctor
        mv.visitVarInsn(Opcodes.ASTORE, 0) // parked in a local — the receiver is several insns away
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitInsn(Opcodes.ICONST_1)
        mv.visitFieldInsn(Opcodes.PUTFIELD, holder, fieldName, "Z") // receiver proves the type: it IS a holder
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitFieldInsn(Opcodes.GETFIELD, holder, fieldName, "Z")
        mv.visitInsn(Opcodes.IRETURN)
        mv.visitMaxs(2, 1)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A GENUINE `new Object()` — a lock: it is monitor-entered, has `Object`'s own `hashCode()` called on it,
     * is stored into an `Object`-typed static and returned as an `Object`. Nothing here pins it to a class of
     * the jar, so the repair must leave it exactly as-is. (Retargeting this would corrupt a working
     * extension: the whole reason the repair is usage-driven and in-jar-gated.)
     */
    private fun genuineObjectUser(self: String): ByteArray {
        val cw = ClassWriter(ClassWriter.COMPUTE_FRAMES)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, self, null, "java/lang/Object", null)
        cw.visitField(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "lock", "Ljava/lang/Object;", null, null).visitEnd()
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "make", "()Ljava/lang/Object;", null, null)
        mv.visitCode()
        mv.visitTypeInsn(Opcodes.NEW, "java/lang/Object")
        mv.visitInsn(Opcodes.DUP)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false)
        mv.visitVarInsn(Opcodes.ASTORE, 0)
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitInsn(Opcodes.MONITORENTER) // used as a lock
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitMethodInsn(Opcodes.INVOKEVIRTUAL, "java/lang/Object", "hashCode", "()I", false) // owner = Object
        mv.visitInsn(Opcodes.POP)
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitInsn(Opcodes.MONITOREXIT)
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitFieldInsn(Opcodes.PUTSTATIC, self, "lock", "Ljava/lang/Object;") // an Object-typed sink
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitMaxs(2, 1)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A collapsed allocation used as the receiver of fields on TWO different holders — impossible in real
     * code, but it is exactly the shape where a usage-driven recovery could guess wrong. The repair must
     * skip it rather than pick one.
     */
    private fun ambiguousUser(
        self: String,
        firstHolder: String,
        secondHolder: String,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, self, null, "java/lang/Object", null)
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "run", "()V", null, null)
        mv.visitCode()
        mv.visitTypeInsn(Opcodes.NEW, "java/lang/Object")
        mv.visitInsn(Opcodes.DUP)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false)
        mv.visitVarInsn(Opcodes.ASTORE, 0)
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitInsn(Opcodes.ICONST_1)
        mv.visitFieldInsn(Opcodes.PUTFIELD, firstHolder, "f", "Z")
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitInsn(Opcodes.ICONST_1)
        mv.visitFieldInsn(Opcodes.PUTFIELD, secondHolder, "g", "Z")
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(2, 1)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** The `desc`s of every `new` in [methodName] of [internalName], read back out of the repaired jar. */
    private fun newInstructionTypes(
        jar: Path,
        internalName: String,
        methodName: String,
    ): List<String> {
        val node = ClassNode()
        ClassReader(classBytesFromJar(jar, internalName)).accept(node, 0)
        return node.methods
            .single { it.name == methodName }
            .instructions
            .toArray()
            .filterIsInstance<TypeInsnNode>()
            .filter { it.opcode == Opcodes.NEW }
            .map { it.desc }
    }

    @Test
    fun `raw dex2jar object collapse fails JVM verification with a bad receiver type`() {
        val loader = BytesLoader()
        loader.define("RawHolder", fieldHolder("RawHolder", "f"))
        // Linking verifies `run()`, whose putfield receiver is a bare java.lang.Object.
        val error =
            assertFailsWith<VerifyError> {
                loader.define("RawUser", collapsedHolderUser("RawUser", "RawHolder", "f")).getDeclaredMethods()
            }
        assertTrue(
            error.message!!.contains("java/lang/Object") && error.message!!.contains("RawHolder"),
            "expected the production 'Type java/lang/Object is not assignable to RawHolder' VerifyError, got: ${error.message}",
        )
    }

    @Test
    fun `repairStackFrames undoes the object collapse and synthesizes the dropped constructor`() {
        val jar =
            jarWithClasses(
                "FixHolder" to fieldHolder("FixHolder", "f"),
                "FixUser" to collapsedHolderUser("FixUser", "FixHolder", "f"),
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertEquals(
            listOf("FixHolder"),
            newInstructionTypes(jar, "FixUser", "run"),
            "the collapsed `new java/lang/Object` must be retargeted to the type its receiver-use implies",
        )
        val loader = BytesLoader()
        val holder = loader.define("FixHolder", classBytesFromJar(jar, "FixHolder"))
        // The retargeted `invokespecial FixHolder.<init>()V` only links if the dropped ctor was synthesized.
        assertTrue(
            holder.declaredConstructors.any { it.parameterCount == 0 },
            "the holder must have regained the no-arg constructor dex2jar dropped",
        )
        val user = loader.define("FixUser", classBytesFromJar(jar, "FixUser"))
        assertEquals(
            true,
            user.getDeclaredMethod("run").invoke(null),
            "the repaired class must verify, link and round-trip the value through the holder's field",
        )
    }

    @Test
    fun `repairStackFrames leaves a genuine new Object untouched`() {
        // A lock/monitor Object has no concrete-class receiver-use, so nothing may retarget it — the gate
        // that keeps this repair from corrupting a WORKING extension.
        val jar = jarWithClasses("Keeper" to genuineObjectUser("Keeper"))

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertEquals(
            listOf("java/lang/Object"),
            newInstructionTypes(jar, "Keeper", "make"),
            "a genuine `new Object()` must survive the repair unchanged",
        )
        val keeper = BytesLoader().define("Keeper", classBytesFromJar(jar, "Keeper"))
        val made = keeper.getDeclaredMethod("make").invoke(null)
        assertEquals(
            "java.lang.Object",
            made!!.javaClass.name,
            "the genuine lock object must still be a plain java.lang.Object at runtime",
        )
    }

    @Test
    fun `repairStackFrames skips a collapsed allocation whose intended type is ambiguous`() {
        // Two different receiver owners for one allocation: unrecoverable, so the repair must not guess.
        val jar =
            jarWithClasses(
                "AmbHolderA" to fieldHolder("AmbHolderA", "f"),
                "AmbHolderB" to fieldHolder("AmbHolderB", "g"),
                "AmbUser" to ambiguousUser("AmbUser", "AmbHolderA", "AmbHolderB"),
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertEquals(
            listOf("java/lang/Object"),
            newInstructionTypes(jar, "AmbUser", "run"),
            "an ambiguous allocation must be left alone rather than retargeted to an arbitrary candidate",
        )
        assertTrue(
            BytesLoader().define("AmbHolderA", classBytesFromJar(jar, "AmbHolderA")).declaredConstructors.isEmpty(),
            "no constructor may be synthesized for a candidate that was never retargeted",
        )
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

    // --- GAP-100 bug (c) regression guards: the object-collapse retarget must be exact-type (FINAL) only,
    //     and single-unmerged-allocation only. The shipped repair was UNSOUND for NON-final recovered types
    //     (MadaraDex/Toonily/Fairy threw ClassCastException in their okhttp interceptors, because the
    //     receiver-use owner it recovered was a SUPERTYPE of the real erased type). The trio below pins the
    //     final-only gate (6) and the no-merge gate (7): a FINAL holder is still retargeted (anisa-shaped
    //     true positive), while a NON-final owner and a merged allocation are both left un-repaired.

    /**
     * A NON-final holder — same shape as [fieldHolder] but subclassable. A value proven to be its receiver
     * is only proven ASSIGNABLE-to it, NOT to be EXACTLY it (a subtype could later be `checkcast`), so gate 6
     * (final-only) must refuse to retarget to it.
     */
    private fun openFieldHolder(
        internalName: String,
        fieldName: String,
    ): ByteArray {
        val cw = ClassWriter(ClassWriter.COMPUTE_FRAMES)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null) // NOT final
        cw.visitField(Opcodes.ACC_PUBLIC, fieldName, "Z", null, null).visitEnd()
        // deliberately NO `<init>` — so a synthesized constructor would be observable if it wrongly retargeted
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A collapsed `new java/lang/Object` whose value MERGES with a foreign producer before its receiver-use:
     * the `then` branch stores the new Object into local 1, the `else` branch stores an `Object`-typed static
     * field into the SAME local, and only AFTER the join is local 1 used as the receiver of [holder]'s field.
     * The value at that use is a phi of two producers, so gate 7 (no-merge) must refuse it as type evidence —
     * we cannot prove THIS allocation always holds a [holder] — and the `new` must stay `java/lang/Object`
     * even though [holder] is final and in-jar.
     */
    private fun mergedAllocationUser(
        self: String,
        holder: String,
        fieldName: String,
    ): ByteArray {
        val cw = ClassWriter(0) // no frames — exactly dex2jar's broken output
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, self, null, "java/lang/Object", null)
        cw.visitField(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "other", "Ljava/lang/Object;", null, null).visitEnd()
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "run", "(I)V", null, null)
        mv.visitCode()
        val elseLabel = Label()
        val endLabel = Label()
        mv.visitVarInsn(Opcodes.ILOAD, 0)
        mv.visitJumpInsn(Opcodes.IFEQ, elseLabel)
        // then: local 1 = new Object (the COLLAPSED allocation)
        mv.visitTypeInsn(Opcodes.NEW, "java/lang/Object")
        mv.visitInsn(Opcodes.DUP)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false)
        mv.visitVarInsn(Opcodes.ASTORE, 1)
        mv.visitJumpInsn(Opcodes.GOTO, endLabel)
        // else: local 1 = a foreign Object-typed value -> merges with the allocation at the join
        mv.visitLabel(elseLabel)
        mv.visitFieldInsn(Opcodes.GETSTATIC, self, "other", "Ljava/lang/Object;")
        mv.visitVarInsn(Opcodes.ASTORE, 1)
        // merge: local 1 is (new Object) phi (other) — provenance is merged, so no retarget
        mv.visitLabel(endLabel)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitInsn(Opcodes.ICONST_1)
        mv.visitFieldInsn(Opcodes.PUTFIELD, holder, fieldName, "Z") // receiver-use — but the value is merged
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(2, 2)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    @Test
    fun `repairStackFrames retargets an object collapse to a FINAL holder (the anisa-shaped positive)`() {
        // The R8 target this repair genuinely needs: a FINAL holder with a field, used as the receiver of the
        // collapsed value. Raw it fails verification; after the repair it retargets, the holder regains the
        // constructor dex2jar dropped, and it runs. This is the case gate 6 MUST keep working.
        val loader = BytesLoader()
        loader.define("FinalHolder", fieldHolder("FinalHolder", "f"))
        assertFailsWith<VerifyError>("raw collapsed class must fail verification") {
            loader.define("FinalUser", collapsedHolderUser("FinalUser", "FinalHolder", "f")).getDeclaredMethods()
        }

        val jar =
            jarWithClasses(
                "FinalHolder" to fieldHolder("FinalHolder", "f"),
                "FinalUser" to collapsedHolderUser("FinalUser", "FinalHolder", "f"),
            )
        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertEquals(
            listOf("FinalHolder"),
            newInstructionTypes(jar, "FinalUser", "run"),
            "a FINAL recovered type is exact, so the collapsed `new java/lang/Object` must be retargeted",
        )
        val fresh = BytesLoader()
        val holder = fresh.define("FinalHolder", classBytesFromJar(jar, "FinalHolder"))
        assertTrue(
            holder.declaredConstructors.any { it.parameterCount == 0 },
            "FinalHolder must have regained the no-arg <init>()V dex2jar dropped",
        )
        val user = fresh.define("FinalUser", classBytesFromJar(jar, "FinalUser"))
        assertEquals(
            true,
            user.getDeclaredMethod("run").invoke(null),
            "the repaired class must verify, link and round-trip the value through the holder's field",
        )
    }

    @Test
    fun `repairStackFrames leaves an object collapse to a NON-final owner un-repaired`() {
        // The regression guard: the receiver-use owner is a NON-final class, so it is only a SUPERTYPE bound —
        // the real erased type could be a subclass and a later checkcast would ClassCast. Gate 6 refuses it.
        val jar =
            jarWithClasses(
                "OpenHolder" to openFieldHolder("OpenHolder", "f"),
                "OpenUser" to collapsedHolderUser("OpenUser", "OpenHolder", "f"),
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertEquals(
            listOf("java/lang/Object"),
            newInstructionTypes(jar, "OpenUser", "run"),
            "a non-final recovered type is only a supertype bound, so the `new` must be left as java/lang/Object",
        )
        assertTrue(
            BytesLoader().define("OpenHolder", classBytesFromJar(jar, "OpenHolder")).declaredConstructors.isEmpty(),
            "no constructor may be synthesized for an owner that was never retargeted",
        )
    }

    @Test
    fun `repairStackFrames leaves a merged object collapse un-repaired`() {
        // The value at the receiver-use is a phi of the `new Object` and a foreign Object-typed field, so we
        // cannot prove THIS allocation always holds the (final, in-jar) holder. Gate 7 refuses it.
        val jar =
            jarWithClasses(
                "MergeHolder" to fieldHolder("MergeHolder", "f"),
                "MergeUser" to mergedAllocationUser("MergeUser", "MergeHolder", "f"),
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertEquals(
            listOf("java/lang/Object"),
            newInstructionTypes(jar, "MergeUser", "run"),
            "a merged (phi) allocation cannot be proven exact, so the `new` must be left as java/lang/Object",
        )
        assertTrue(
            BytesLoader().define("MergeHolder", classBytesFromJar(jar, "MergeHolder")).declaredConstructors.isEmpty(),
            "no constructor may be synthesized for a holder whose allocation was never retargeted",
        )
    }

    // --- GAP-100 universal ctor backfill: dex2jar drops the constructor of R8-optimized lambda/enum/holder
    //     classes, leaving them with methods but no `<init>`. The vendored dex2jar fix now makes the translator
    //     emit the CORRECT allocation type directly (`new k0; invokespecial k0.<init>()V`) instead of collapsing
    //     to `new java/lang/Object`, but k0's own `<init>` is still missing and NOTHING retargets that `new`
    //     (it is not a self-instantiation and not an Object collapse), so bug (b)/(c) never synthesize it and the
    //     class fails to link with `NoSuchMethodError: k0.<init>()`. The backfill pass synthesizes a forwarding
    //     ctor for EVERY dangling in-jar `invokespecial <init>`, guarded on the super actually declaring the same
    //     descriptor so it can never relocate the error onto its own super-call. The trio below pins that.

    /** A class with a real `<init>(I)V` that chains to `Object` — the in-jar super a descriptor-carrying ctor forwards to. */
    private fun baseWithIntCtor(internalName: String): ByteArray {
        val cw = ClassWriter(ClassWriter.COMPUTE_FRAMES)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC, "<init>", "(I)V", null, null)
        mv.visitCode()
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false)
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(1, 2)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A class `X extends [superName]` with NO `<init>` at all — exactly what dex2jar leaves for an R8
     * lambda/enum/holder singleton once the vendored type-fix has restored the correct `new X` allocation but
     * the constructor is still dropped.
     */
    private fun ctorlessClass(
        internalName: String,
        superName: String,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, superName, null)
        // deliberately NO `<init>` — dex2jar dropped it
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * A class whose static `make()` allocates [target] with the CORRECT type and the given ctor [desc] —
     * `new target; dup; <push args>; invokespecial target.<init>[desc]; …` — modelling the vendored dex2jar
     * fix's output. [pushArgs] emits the constructor arguments; the result is returned (`areturn`) or, for a
     * void method (`retDesc == "()V"`), discarded (`pop; return`) so a class we only inspect stays valid.
     */
    private fun allocator(
        self: String,
        target: String,
        desc: String,
        retDesc: String,
        pushArgs: (MethodVisitor) -> Unit,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, self, null, "java/lang/Object", null)
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "make", retDesc, null, null)
        mv.visitCode()
        mv.visitTypeInsn(Opcodes.NEW, target)
        mv.visitInsn(Opcodes.DUP)
        pushArgs(mv)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, target, "<init>", desc, false)
        if (retDesc == "()V") {
            mv.visitInsn(Opcodes.POP)
            mv.visitInsn(Opcodes.RETURN)
        } else {
            mv.visitInsn(Opcodes.ARETURN)
        }
        mv.visitMaxs(4, 0)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** A final direct subclass whose constructor was dropped from dex2jar's JVM output. */
    private fun ctorlessDirectSubclass(
        internalName: String,
        superName: String,
        access: Int = Opcodes.ACC_PUBLIC or Opcodes.ACC_FINAL,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, access, internalName, null, superName, null)
        cw.visitEnd()
        return cw.toByteArray()
    }

    /**
     * The Comix-shaped JVM defect: `new X` remains on the operand stack while a duplicate is parked in a
     * local and the constructor argument is prepared, then the uninitialized X is incorrectly passed to
     * `X`'s direct superclass constructor. Android accepts this R8 output; the JVM requires X.<init>.
     */
    private fun wrongDirectSuperCtorUser(
        internalName: String,
        allocationType: String,
        constructorOwner: String,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv =
            cw.visitMethod(
                Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC,
                "make",
                "(Ljava/lang/String;)Ljava/lang/Object;",
                null,
                null,
            )
        mv.visitCode()
        mv.visitTypeInsn(Opcodes.NEW, allocationType)
        mv.visitInsn(Opcodes.DUP)
        mv.visitVarInsn(Opcodes.ASTORE, 1)
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitMethodInsn(
            Opcodes.INVOKESPECIAL,
            constructorOwner,
            "<init>",
            "(Ljava/lang/String;)V",
            false,
        )
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitMaxs(2, 2)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** A direct subclass with a real same-descriptor constructor, either trivial or observably non-trivial. */
    private fun directSubclassWithStringCtor(
        internalName: String,
        superName: String,
        trivial: Boolean,
        constructorAccess: Int = Opcodes.ACC_PUBLIC,
    ): ByteArray {
        val cw = ClassWriter(ClassWriter.COMPUTE_FRAMES)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC or Opcodes.ACC_FINAL, internalName, null, superName, null)
        if (!trivial) cw.visitField(Opcodes.ACC_PUBLIC, "touched", "Z", null, null).visitEnd()
        val mv = cw.visitMethod(constructorAccess, "<init>", "(Ljava/lang/String;)V", null, null)
        mv.visitCode()
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, superName, "<init>", "(Ljava/lang/String;)V", false)
        if (!trivial) {
            mv.visitVarInsn(Opcodes.ALOAD, 0)
            mv.visitInsn(Opcodes.ICONST_1)
            mv.visitFieldInsn(Opcodes.PUTFIELD, internalName, "touched", "Z")
        }
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(2, 2)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** A constructor whose valid direct-super call uses uninitialized `this`; the later NEW forces analysis. */
    private fun constructorChainingClass(internalName: String): ByteArray {
        val cw = ClassWriter(ClassWriter.COMPUTE_FRAMES)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC or Opcodes.ACC_FINAL, internalName, null, "java/lang/Exception", null)
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC, "<init>", "(Ljava/lang/String;)V", null, null)
        mv.visitCode()
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitMethodInsn(
            Opcodes.INVOKESPECIAL,
            "java/lang/Exception",
            "<init>",
            "(Ljava/lang/String;)V",
            false,
        )
        mv.visitTypeInsn(Opcodes.NEW, "java/lang/Object")
        mv.visitInsn(Opcodes.DUP)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false)
        mv.visitInsn(Opcodes.POP)
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(2, 2)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** A subclass constructor that illegally skips its direct superclass and invokes an ancestor constructor. */
    private fun ancestorBypassConstructor(
        internalName: String,
        directSuper: String,
        constructorOwner: String,
        repeatInitializer: Boolean = false,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC or Opcodes.ACC_FINAL, internalName, null, directSuper, null)
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC, "<init>", "(Ljava/lang/String;)V", null, null)
        mv.visitCode()
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, constructorOwner, "<init>", "(Ljava/lang/String;)V", false)
        if (repeatInitializer) {
            mv.visitVarInsn(Opcodes.ALOAD, 0)
            mv.visitVarInsn(Opcodes.ALOAD, 1)
            mv.visitMethodInsn(Opcodes.INVOKESPECIAL, constructorOwner, "<init>", "(Ljava/lang/String;)V", false)
        }
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(2, 2)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** A non-final hierarchy member with a same-descriptor constructor used by ancestor-bypass fixtures. */
    private fun hierarchyClassWithStringCtor(
        internalName: String,
        superName: String,
        classAccess: Int,
        constructorAccess: Int = Opcodes.ACC_PUBLIC,
        trivial: Boolean = true,
    ): ByteArray {
        val cw = ClassWriter(ClassWriter.COMPUTE_FRAMES)
        cw.visit(Opcodes.V1_8, classAccess, internalName, null, superName, null)
        if (!trivial) cw.visitField(Opcodes.ACC_PUBLIC, "touched", "Z", null, null).visitEnd()
        val mv = cw.visitMethod(constructorAccess, "<init>", "(Ljava/lang/String;)V", null, null)
        mv.visitCode()
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, superName, "<init>", "(Ljava/lang/String;)V", false)
        if (!trivial) {
            mv.visitVarInsn(Opcodes.ALOAD, 0)
            mv.visitInsn(Opcodes.ICONST_1)
            mv.visitFieldInsn(Opcodes.PUTFIELD, internalName, "touched", "Z")
        }
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(2, 2)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** Two control-flow branches initialize the same allocation; there is no unique initializer to retarget. */
    private fun multipleInitializerUser(
        internalName: String,
        allocationType: String,
        constructorOwner: String,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv =
            cw.visitMethod(
                Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC,
                "make",
                "(ZLjava/lang/String;)Ljava/lang/Object;",
                null,
                null,
            )
        mv.visitCode()
        val second = Label()
        val done = Label()
        mv.visitTypeInsn(Opcodes.NEW, allocationType)
        mv.visitInsn(Opcodes.DUP)
        mv.visitVarInsn(Opcodes.ASTORE, 2)
        mv.visitVarInsn(Opcodes.ILOAD, 0)
        mv.visitJumpInsn(Opcodes.IFEQ, second)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, constructorOwner, "<init>", "(Ljava/lang/String;)V", false)
        mv.visitJumpInsn(Opcodes.GOTO, done)
        mv.visitLabel(second)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, constructorOwner, "<init>", "(Ljava/lang/String;)V", false)
        mv.visitLabel(done)
        mv.visitVarInsn(Opcodes.ALOAD, 2)
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitMaxs(2, 3)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** Two allocations merge into one constructor receiver; neither NEW has exact, unmerged provenance. */
    private fun mergedNewUser(
        internalName: String,
        allocationType: String,
        constructorOwner: String,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv =
            cw.visitMethod(
                Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC,
                "make",
                "(ZLjava/lang/String;)Ljava/lang/Object;",
                null,
                null,
            )
        mv.visitCode()
        val second = Label()
        val merged = Label()
        mv.visitVarInsn(Opcodes.ILOAD, 0)
        mv.visitJumpInsn(Opcodes.IFEQ, second)
        mv.visitTypeInsn(Opcodes.NEW, allocationType)
        mv.visitInsn(Opcodes.DUP)
        mv.visitVarInsn(Opcodes.ASTORE, 2)
        mv.visitJumpInsn(Opcodes.GOTO, merged)
        mv.visitLabel(second)
        mv.visitTypeInsn(Opcodes.NEW, allocationType)
        mv.visitInsn(Opcodes.DUP)
        mv.visitVarInsn(Opcodes.ASTORE, 2)
        mv.visitLabel(merged)
        mv.visitVarInsn(Opcodes.ALOAD, 1)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, constructorOwner, "<init>", "(Ljava/lang/String;)V", false)
        mv.visitVarInsn(Opcodes.ALOAD, 2)
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitMaxs(2, 3)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** A NEW without any constructor receiver; the repair must not invent an initializer. */
    private fun noInitializerUser(
        internalName: String,
        allocationType: String,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC, "make", "()V", null, null)
        mv.visitCode()
        mv.visitTypeInsn(Opcodes.NEW, allocationType)
        mv.visitInsn(Opcodes.POP)
        mv.visitInsn(Opcodes.RETURN)
        mv.visitMaxs(1, 0)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** A malformed method whose stack underflow makes ASM dataflow fail before a tempting wrong-owner pair. */
    private fun analyzerFailureUser(
        internalName: String,
        allocationType: String,
        constructorOwner: String,
    ): ByteArray {
        val cw = ClassWriter(0)
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv =
            cw.visitMethod(
                Opcodes.ACC_PUBLIC or Opcodes.ACC_STATIC,
                "make",
                "(Ljava/lang/String;)Ljava/lang/Object;",
                null,
                null,
            )
        mv.visitCode()
        mv.visitInsn(Opcodes.POP)
        mv.visitTypeInsn(Opcodes.NEW, allocationType)
        mv.visitInsn(Opcodes.DUP)
        mv.visitVarInsn(Opcodes.ALOAD, 0)
        mv.visitMethodInsn(Opcodes.INVOKESPECIAL, constructorOwner, "<init>", "(Ljava/lang/String;)V", false)
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitMaxs(2, 1)
        mv.visitEnd()
        cw.visitEnd()
        return cw.toByteArray()
    }

    /** Invoke only the constructor-owner stage and prove every named class remained byte-for-byte unchanged. */
    private fun assertConstructorOwnerRepairSkipsByteExactly(
        jar: Path,
        vararg internalNames: String,
    ) {
        val before = internalNames.associateWith { classBytesFromJar(jar, it) }
        DexStackFrameRewriter.repairInvalidConstructorOwners(jar, javaClass.classLoader)
        for (name in internalNames) {
            assertContentEquals(before.getValue(name), classBytesFromJar(jar, name), "$name must remain byte-identical")
        }
    }

    /** The owners named by every constructor invocation in [methodName]. */
    private fun constructorOwners(
        jar: Path,
        internalName: String,
        methodName: String,
    ): List<String> {
        val node = ClassNode()
        ClassReader(classBytesFromJar(jar, internalName)).accept(node, 0)
        return node.methods
            .single { it.name == methodName }
            .instructions
            .toArray()
            .filterIsInstance<MethodInsnNode>()
            .filter { it.opcode == Opcodes.INVOKESPECIAL && it.name == "<init>" }
            .map { it.owner }
    }

    @Test
    fun `repairStackFrames retargets a non-adjacent direct-super constructor call and makes it load`() {
        val allocationType = "CtorOwnerChild"
        val caller = "CtorOwnerCaller"
        val rawChild = ctorlessDirectSubclass(allocationType, "java/lang/Exception")
        val rawCaller = wrongDirectSuperCtorUser(caller, allocationType, "java/lang/Exception")
        val rawLoader = BytesLoader()
        rawLoader.define(allocationType, rawChild)
        val rawError =
            assertFailsWith<VerifyError> {
                rawLoader.define(caller, rawCaller).getDeclaredMethod("make", String::class.java)
            }
        assertTrue(
            rawError.message!!.contains("wrong <init>") || rawError.message!!.contains("not assignable"),
            "the fixture must reproduce the constructor-owner verifier failure, got: ${rawError.message}",
        )

        val jar = jarWithClasses(allocationType to rawChild, caller to rawCaller)

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertEquals(
            listOf(allocationType),
            constructorOwners(jar, caller, "make"),
            "the initializer must target the exact class allocated by NEW",
        )
        val loader = BytesLoader()
        loader.define(allocationType, classBytesFromJar(jar, allocationType))
        val repairedCaller = loader.define(caller, classBytesFromJar(jar, caller))
        val made = repairedCaller.getDeclaredMethod("make", String::class.java).invoke(null, "comix") as Exception
        assertEquals(allocationType, made.javaClass.name, "the repaired bytecode must construct the allocated subclass")
        assertEquals("comix", made.message, "the synthesized same-descriptor constructor must forward its argument")
    }

    @Test
    fun `constructor-owner repair accepts an existing trivial same-descriptor forwarder`() {
        val child = "ExistingTrivialChild"
        val caller = "ExistingTrivialCaller"
        val childBytes = directSubclassWithStringCtor(child, "java/lang/Exception", trivial = true)
        val jar = jarWithClasses(child to childBytes, caller to wrongDirectSuperCtorUser(caller, child, "java/lang/Exception"))

        DexStackFrameRewriter.repairInvalidConstructorOwners(jar, javaClass.classLoader)

        assertEquals(listOf(child), constructorOwners(jar, caller, "make"))
        assertContentEquals(
            childBytes,
            classBytesFromJar(jar, child),
            "an already-safe target constructor must not be rewritten",
        )
    }

    @Test
    fun `constructor-owner repair skips a nontrivial existing constructor byte-for-byte`() {
        val child = "ExistingNontrivialChild"
        val caller = "ExistingNontrivialCaller"
        val jar =
            jarWithClasses(
                child to directSubclassWithStringCtor(child, "java/lang/Exception", trivial = false),
                caller to wrongDirectSuperCtorUser(caller, child, "java/lang/Exception"),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, child, caller)
    }

    @Test
    fun `constructor-owner repair skips an inaccessible existing constructor byte-for-byte`() {
        val child = "PrivateCtorChild"
        val caller = "PrivateCtorCaller"
        val jar =
            jarWithClasses(
                child to
                    directSubclassWithStringCtor(
                        child,
                        "java/lang/Exception",
                        trivial = true,
                        constructorAccess = Opcodes.ACC_PRIVATE,
                    ),
                caller to wrongDirectSuperCtorUser(caller, child, "java/lang/Exception"),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, child, caller)
    }

    @Test
    fun `constructor-owner repair skips merged two-NEW provenance byte-for-byte`() {
        val child = "MergedCtorChild"
        val caller = "MergedCtorCaller"
        val jar =
            jarWithClasses(
                child to ctorlessDirectSubclass(child, "java/lang/Exception"),
                caller to mergedNewUser(caller, child, "java/lang/Exception"),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, child, caller)
    }

    @Test
    fun `constructor-owner repair skips multiple and missing initializers byte-for-byte`() {
        val child = "InitCountChild"
        val multiple = "MultipleInitCaller"
        val missing = "MissingInitCaller"
        val jar =
            jarWithClasses(
                child to ctorlessDirectSubclass(child, "java/lang/Exception"),
                multiple to multipleInitializerUser(multiple, child, "java/lang/Exception"),
                missing to noInitializerUser(missing, child),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, child, multiple, missing)
    }

    @Test
    fun `constructor-owner repair skips an indirect ancestor owner byte-for-byte`() {
        val middle = "IndirectCtorMiddle"
        val child = "IndirectCtorChild"
        val caller = "IndirectCtorCaller"
        val jar =
            jarWithClasses(
                middle to directSubclassWithStringCtor(middle, "java/lang/Exception", trivial = true),
                child to ctorlessDirectSubclass(child, middle),
                caller to wrongDirectSuperCtorUser(caller, child, "java/lang/Exception"),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, middle, child, caller)
    }

    @Test
    fun `constructor-owner repair skips a missing same-descriptor super constructor byte-for-byte`() {
        val parent = "MissingSuperCtorParent"
        val child = "MissingSuperCtorChild"
        val caller = "MissingSuperCtorCaller"
        val jar =
            jarWithClasses(
                parent to ctorlessDirectSubclass(parent, "java/lang/Object"),
                child to ctorlessDirectSubclass(child, parent),
                caller to wrongDirectSuperCtorUser(caller, child, parent),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, parent, child, caller)
    }

    @Test
    fun `constructor-owner repair skips an out-of-jar allocation type byte-for-byte`() {
        val caller = "ExternalCtorCaller"
        val jar = jarWith(caller, wrongDirectSuperCtorUser(caller, "java/io/IOException", "java/lang/Exception"))

        assertConstructorOwnerRepairSkipsByteExactly(jar, caller)
    }

    @Test
    fun `constructor-owner repair leaves valid NEW X and X init byte-for-byte unchanged`() {
        val child = "ValidCtorChild"
        val caller = "ValidCtorCaller"
        val jar =
            jarWithClasses(
                child to directSubclassWithStringCtor(child, "java/lang/Exception", trivial = true),
                caller to wrongDirectSuperCtorUser(caller, child, child),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, child, caller)
    }

    @Test
    fun `constructor-owner repair leaves constructor uninitialized-this chaining byte-for-byte unchanged`() {
        val child = "UninitializedThisChild"
        val jar = jarWith(child, constructorChainingClass(child))

        assertConstructorOwnerRepairSkipsByteExactly(jar, child)
        assertEquals(
            listOf("java/lang/Exception", "java/lang/Object"),
            constructorOwners(jar, child, "<init>"),
            "the uninitialized-this chain and independent valid allocation must both remain unchanged",
        )
    }

    @Test
    fun `repairStackFrames repairs an ancestor constructor bypass through a ctorless abstract middle`() {
        val base = "AncestorBase"
        val middle = "AncestorMiddle"
        val leaf = "AncestorLeaf"
        val rawBase =
            hierarchyClassWithStringCtor(base, "java/lang/Exception", Opcodes.ACC_PUBLIC, trivial = true)
        val rawMiddle = ctorlessDirectSubclass(middle, base, Opcodes.ACC_PUBLIC or Opcodes.ACC_ABSTRACT)
        val rawLeaf = ancestorBypassConstructor(leaf, middle, base)
        val rawLoader = BytesLoader()
        rawLoader.define(base, rawBase)
        rawLoader.define(middle, rawMiddle)
        val rawError =
            assertFailsWith<VerifyError> {
                rawLoader.define(leaf, rawLeaf).getConstructor(String::class.java)
            }
        assertTrue(
            rawError.message!!.contains("wrong <init>") || rawError.message!!.contains("not assignable"),
            "the fixture must reproduce the ancestor-constructor verifier failure, got: ${rawError.message}",
        )

        val jar = jarWithClasses(base to rawBase, middle to rawMiddle, leaf to rawLeaf)

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertEquals(listOf(middle), constructorOwners(jar, leaf, "<init>"))
        assertEquals(listOf(base), constructorOwners(jar, middle, "<init>"))
        val loader = BytesLoader()
        loader.define(base, classBytesFromJar(jar, base))
        loader.define(middle, classBytesFromJar(jar, middle))
        val repairedLeaf = loader.define(leaf, classBytesFromJar(jar, leaf))
        val made = repairedLeaf.getConstructor(String::class.java).newInstance("message") as Exception
        assertEquals("message", made.message)

        val first = listOf(base, middle, leaf).associateWith { classBytesFromJar(jar, it) }
        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)
        for (name in first.keys) {
            assertContentEquals(first.getValue(name), classBytesFromJar(jar, name), "$name must be stable on pass two")
        }
    }

    @Test
    fun `ancestor constructor repair uses an existing trivial direct-super constructor`() {
        val base = "ExistingAncestorBase"
        val middle = "ExistingAncestorMiddle"
        val leaf = "ExistingAncestorLeaf"
        val middleBytes =
            hierarchyClassWithStringCtor(
                middle,
                base,
                Opcodes.ACC_PUBLIC or Opcodes.ACC_ABSTRACT,
                trivial = true,
            )
        val jar =
            jarWithClasses(
                base to hierarchyClassWithStringCtor(base, "java/lang/Exception", Opcodes.ACC_PUBLIC),
                middle to middleBytes,
                leaf to ancestorBypassConstructor(leaf, middle, base),
            )

        DexStackFrameRewriter.repairInvalidConstructorOwners(jar, javaClass.classLoader)

        assertEquals(listOf(middle), constructorOwners(jar, leaf, "<init>"))
        assertContentEquals(middleBytes, classBytesFromJar(jar, middle))
    }

    @Test
    fun `ancestor constructor repair skips a nontrivial intermediate constructor byte-for-byte`() {
        val base = "UnsafeAncestorBase"
        val middle = "UnsafeAncestorMiddle"
        val leaf = "UnsafeAncestorLeaf"
        val jar =
            jarWithClasses(
                base to hierarchyClassWithStringCtor(base, "java/lang/Exception", Opcodes.ACC_PUBLIC),
                middle to
                    hierarchyClassWithStringCtor(
                        middle,
                        base,
                        Opcodes.ACC_PUBLIC or Opcodes.ACC_ABSTRACT,
                        trivial = false,
                    ),
                leaf to ancestorBypassConstructor(leaf, middle, base),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, base, middle, leaf)
    }

    @Test
    fun `ancestor constructor repair skips an inaccessible intermediate constructor byte-for-byte`() {
        val base = "PrivateAncestorBase"
        val middle = "PrivateAncestorMiddle"
        val leaf = "PrivateAncestorLeaf"
        val jar =
            jarWithClasses(
                base to hierarchyClassWithStringCtor(base, "java/lang/Exception", Opcodes.ACC_PUBLIC),
                middle to
                    hierarchyClassWithStringCtor(
                        middle,
                        base,
                        Opcodes.ACC_PUBLIC or Opcodes.ACC_ABSTRACT,
                        constructorAccess = Opcodes.ACC_PRIVATE,
                    ),
                leaf to ancestorBypassConstructor(leaf, middle, base),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, base, middle, leaf)
    }

    @Test
    fun `ancestor constructor repair skips multiple uninitialized-this initializers byte-for-byte`() {
        val base = "AmbiguousAncestorBase"
        val middle = "AmbiguousAncestorMiddle"
        val leaf = "AmbiguousAncestorLeaf"
        val jar =
            jarWithClasses(
                base to hierarchyClassWithStringCtor(base, "java/lang/Exception", Opcodes.ACC_PUBLIC),
                middle to ctorlessDirectSubclass(middle, base, Opcodes.ACC_PUBLIC or Opcodes.ACC_ABSTRACT),
                leaf to ancestorBypassConstructor(leaf, middle, base, repeatInitializer = true),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, base, middle, leaf)
    }

    @Test
    fun `constructor-owner repair skips non-instantiable allocation targets byte-for-byte`() {
        val child = "AbstractCtorChild"
        val caller = "AbstractCtorCaller"
        val jar =
            jarWithClasses(
                child to
                    ctorlessDirectSubclass(
                        child,
                        "java/lang/Exception",
                        Opcodes.ACC_PUBLIC or Opcodes.ACC_ABSTRACT,
                    ),
                caller to wrongDirectSuperCtorUser(caller, child, "java/lang/Exception"),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, child, caller)
    }

    @Test
    fun `constructor-owner repair keeps analyzer failures byte-for-byte unchanged`() {
        val child = "AnalyzerFailureChild"
        val caller = "AnalyzerFailureCaller"
        val jar =
            jarWithClasses(
                child to ctorlessDirectSubclass(child, "java/lang/Exception"),
                caller to analyzerFailureUser(caller, child, "java/lang/Exception"),
            )

        assertConstructorOwnerRepairSkipsByteExactly(jar, child, caller)
    }

    @Test
    fun `constructor-owner repair is idempotent`() {
        val child = "IdempotentCtorChild"
        val caller = "IdempotentCtorCaller"
        val jar =
            jarWithClasses(
                child to ctorlessDirectSubclass(child, "java/lang/Exception"),
                caller to wrongDirectSuperCtorUser(caller, child, "java/lang/Exception"),
            )

        DexStackFrameRewriter.repairInvalidConstructorOwners(jar, javaClass.classLoader)
        val first = listOf(child, caller).associateWith { classBytesFromJar(jar, it) }
        DexStackFrameRewriter.repairInvalidConstructorOwners(jar, javaClass.classLoader)

        for (name in listOf(child, caller)) {
            assertContentEquals(first.getValue(name), classBytesFromJar(jar, name), "$name must be stable on pass two")
        }
    }

    /**
     * A ctorless class (dex2jar dropped its `<init>`) that ALSO carries a branchy method: an `IFNE` to a label
     * whose target needs a StackMapTable frame, emitted with `ClassWriter(0)` so NO frames are present. It is the
     * shape that makes the backfill's serialization choice observable — the touched class must be written with
     * RECOMPUTED frames (not stripped with COMPUTE_MAXS), so it verifies on load rather than relying on a later
     * pass. Raw (frameless branch at v52) it would fail verification; after the repair its `label(int)` must run.
     */
    private fun ctorlessBranchyClass(internalName: String): ByteArray {
        val cw = ClassWriter(0) // no frames + no ctor — dex2jar's dropped-ctor output with a frameless branch
        cw.visit(Opcodes.V1_8, Opcodes.ACC_PUBLIC, internalName, null, "java/lang/Object", null)
        val mv = cw.visitMethod(Opcodes.ACC_PUBLIC, "label", "(I)Ljava/lang/String;", null, null)
        mv.visitCode()
        val elseLabel = Label()
        mv.visitVarInsn(Opcodes.ILOAD, 1)
        mv.visitJumpInsn(Opcodes.IFNE, elseLabel) // branch whose target needs a stackmap frame
        mv.visitLdcInsn("zero")
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitLabel(elseLabel) // <- strict verifier demands a frame here
        mv.visitLdcInsn("nonzero")
        mv.visitInsn(Opcodes.ARETURN)
        mv.visitMaxs(1, 2)
        mv.visitEnd()
        // deliberately NO `<init>` — dex2jar dropped it
        cw.visitEnd()
        return cw.toByteArray()
    }

    @Test
    fun `repairStackFrames backfills a dropped no-arg ctor for an in-jar new target`() {
        // The vendored dex2jar fix emits the correct `new BfB; invokespecial BfB.<init>()V`, but BfB's ctor is
        // still dropped. Nothing retargets this `new` (BfB is neither its allocator's superclass nor
        // java/lang/Object), so only the universal backfill can restore BfB.<init>()V; without it BfB fails to
        // link with NoSuchMethodError.
        val jar =
            jarWithClasses(
                "BfB" to ctorlessClass("BfB", "java/lang/Object"),
                "BfA" to allocator("BfA", "BfB", "()V", "()Ljava/lang/Object;") {},
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        val loader = BytesLoader()
        val b = loader.define("BfB", classBytesFromJar(jar, "BfB"))
        assertTrue(
            b.declaredConstructors.any { it.parameterCount == 0 },
            "the backfill must give BfB the no-arg <init>()V dex2jar dropped",
        )
        val a = loader.define("BfA", classBytesFromJar(jar, "BfA"))
        // Links and runs only because BfB.<init>()V now exists.
        assertEquals(
            "BfB",
            a.getDeclaredMethod("make").invoke(null)!!.javaClass.name,
            "the allocator must construct a real BfB through the backfilled constructor",
        )
    }

    @Test
    fun `repairStackFrames backfills a dropped descriptor ctor forwarding to an in-jar super`() {
        // BfD extends BfBase, which DOES declare <init>(I)V; BfD's own <init>(I)V was dropped. The backfill must
        // synthesize BfD.<init>(I)V forwarding the same descriptor to BfBase.
        val jar =
            jarWithClasses(
                "BfBase" to baseWithIntCtor("BfBase"),
                "BfD" to ctorlessClass("BfD", "BfBase"),
                "BfC" to allocator("BfC", "BfD", "(I)V", "()Ljava/lang/Object;") { it.visitInsn(Opcodes.ICONST_0) },
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        val loader = BytesLoader()
        loader.define("BfBase", classBytesFromJar(jar, "BfBase"))
        val d = loader.define("BfD", classBytesFromJar(jar, "BfD"))
        assertTrue(
            d.declaredConstructors.any { it.parameterCount == 1 && it.parameterTypes[0] == Int::class.javaPrimitiveType },
            "the backfill must give BfD the <init>(I)V it forwards to its base",
        )
        val c = loader.define("BfC", classBytesFromJar(jar, "BfC"))
        assertEquals(
            "BfD",
            c.getDeclaredMethod("make").invoke(null)!!.javaClass.name,
            "the allocator must construct a real BfD through the backfilled (I)V constructor",
        )
    }

    @Test
    fun `repairStackFrames leaves a dangling ctor whose super lacks it un-backfilled`() {
        // BfF extends java/lang/Object, which declares no <init>(Ljava/lang/String;)V. Synthesizing
        // BfF.<init>(Ljava/lang/String;)V would merely relocate the NoSuchMethodError to its own super-call, so
        // the super-resolvability guard MUST skip it and leave BfF byte-for-byte unchanged.
        val jar =
            jarWithClasses(
                "BfF" to ctorlessClass("BfF", "java/lang/Object"),
                "BfE" to
                    allocator("BfE", "BfF", "(Ljava/lang/String;)V", "()V") { it.visitInsn(Opcodes.ACONST_NULL) },
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        assertTrue(
            BytesLoader().define("BfF", classBytesFromJar(jar, "BfF")).declaredConstructors.isEmpty(),
            "no ctor may be synthesized when the super declares no matching <init> (would only relocate the error)",
        )
    }

    @Test
    fun `repairStackFrames backfills a ctor for a class whose branchy method needs recomputed frames`() {
        // The touched class RECEIVES a backfilled <init>()V AND carries a branchy method (IFNE to a label) with
        // NO StackMapTable — the frames must be recomputed. The backfill now serializes touched classes with
        // recomputed frames (FrameComputingClassWriter, not COMPUTE_MAXS), so the on-disk bytes are frame-valid
        // even if a later recompute would fail; the class must VERIFY and run its branch, not just gain the ctor.
        val jar =
            jarWithClasses(
                "BgBranchy" to ctorlessBranchyClass("BgBranchy"),
                "BgA" to allocator("BgA", "BgBranchy", "()V", "()Ljava/lang/Object;") {},
            )

        DexStackFrameRewriter.repairStackFrames(jar, javaClass.classLoader)

        val loader = BytesLoader()
        val branchy = loader.define("BgBranchy", classBytesFromJar(jar, "BgBranchy"))
        assertTrue(
            branchy.declaredConstructors.any { it.parameterCount == 0 },
            "the backfill must give BgBranchy the no-arg <init>()V dex2jar dropped",
        )
        // newInstance() links + verifies the class: the branchy method only passes with valid recomputed frames.
        val instance = branchy.getDeclaredConstructor().newInstance()
        val label = branchy.getDeclaredMethod("label", Int::class.javaPrimitiveType)
        assertEquals("zero", label.invoke(instance, 0), "the branchy method must verify and run its then-branch")
        assertEquals("nonzero", label.invoke(instance, 7), "the branchy method must verify and run its else-branch")
        // The allocator links + runs only because BgBranchy.<init>()V now exists.
        val a = loader.define("BgA", classBytesFromJar(jar, "BgA"))
        assertEquals(
            "BgBranchy",
            a.getDeclaredMethod("make").invoke(null)!!.javaClass.name,
            "the allocator must construct a real BgBranchy through the backfilled constructor",
        )
    }
}
