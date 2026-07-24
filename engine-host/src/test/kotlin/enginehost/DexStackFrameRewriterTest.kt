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
import org.objectweb.asm.tree.TypeInsnNode
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
}
