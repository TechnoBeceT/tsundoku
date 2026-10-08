package enginehost

import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Opcodes.*
import java.net.URLClassLoader
import java.lang.reflect.InvocationTargetException
import kotlinx.serialization.MissingFieldException
import kotlinx.serialization.descriptors.SerialDescriptor
import java.nio.file.Files
import java.util.jar.JarEntry
import java.util.jar.JarOutputStream
import kotlin.io.path.createTempDirectory
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlin.test.assertContentEquals
import kotlin.test.assertFailsWith

class ComixCompatibilityTest {
    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `transformed serialization constructor defaults only missing base and keeps items required`() {
        val root = createTempDirectory("comix-constructor")
        try {
            val jar = root.resolve("fixture.jar")
            fixture(jar)
            ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.42", 106042)
            URLClassLoader(arrayOf(jar.toUri().toURL()), javaClass.classLoader).use { loader ->
                val item = loader.loadClass("FixtureItem").getConstructor(Int::class.javaPrimitiveType, String::class.java, Int::class.javaPrimitiveType)
                val items = listOf("https://images.example/one.jpg", "http://images.example/two.jpg").map { item.newInstance(1, it, 0) }
                val pages = loader.loadClass("FixturePages")
                val ctor = pages.getConstructor(Int::class.javaPrimitiveType, String::class.java, List::class.java)
                val decoded = ctor.newInstance(2, null, items)
                assertEquals("", pages.getField("base").get(decoded))
                assertEquals(items, pages.getField("items").get(decoded))
                val relative = listOf(item.newInstance(1, "one.jpg", 0))
                val old = ctor.newInstance(3, "https://images.example", relative)
                assertEquals("https://images.example", pages.getField("base").get(old))
                assertEquals(relative, pages.getField("items").get(old))
                val missingBase = assertFailsWith<InvocationTargetException> { ctor.newInstance(2, null, relative) }
                assertTrue(missingBase.cause is IllegalArgumentException)
                val missingItems = assertFailsWith<InvocationTargetException> { ctor.newInstance(0, null, null) }
                assertTrue(missingItems.cause is MissingFieldException)
                val descriptor = loader.loadClass("FixtureSerializer").getField("descriptor").get(null) as SerialDescriptor
                assertTrue(descriptor.isElementOptional(0))
                assertTrue(!descriptor.isElementOptional(1))
            }
        } finally {
            root.toFile().deleteRecursively()
        }
    }

    // Mirrors the independent Kotlin serialization contract: bit 1 requires base, bit 2 items.
    // Actual released-APK parser/page-builder verification supplements this deterministic fixture.
    private fun fixture(jar: java.nio.file.Path) {
        val classes = linkedMapOf<String, ByteArray>()
        fun type(name: String, body: (ClassWriter) -> Unit) {
            val writer = ClassWriter(ClassWriter.COMPUTE_FRAMES or ClassWriter.COMPUTE_MAXS)
            writer.visit(V1_8, ACC_PUBLIC, name, null, "java/lang/Object", null)
            body(writer)
            writer.visitEnd()
            classes[name] = writer.toByteArray()
        }
        type("FixtureItem") { writer ->
            writer.visitField(ACC_PUBLIC, "url", "Ljava/lang/String;", null, null).visitEnd()
            writer.visitMethod(ACC_PUBLIC, "<init>", "(ILjava/lang/String;I)V", null, null).apply {
                visitCode(); visitVarInsn(ALOAD, 0); visitMethodInsn(INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false)
                visitVarInsn(ALOAD, 0); visitVarInsn(ALOAD, 2); visitFieldInsn(PUTFIELD, "FixtureItem", "url", "Ljava/lang/String;")
                visitInsn(RETURN); visitMaxs(0, 0); visitEnd()
            }
        }
        type("FixturePages") { writer ->
            writer.visitField(ACC_PUBLIC, "base", "Ljava/lang/String;", null, null).visitEnd()
            writer.visitField(ACC_PUBLIC, "items", "Ljava/util/List;", null, null).visitEnd()
            writer.visitMethod(ACC_PUBLIC, "<init>", "(ILjava/lang/String;Ljava/util/List;)V", null, null).apply {
                visitCode(); visitInsn(ICONST_3); visitVarInsn(ILOAD, 1); visitInsn(ICONST_3); visitInsn(IAND)
                val valid = org.objectweb.asm.Label(); visitJumpInsn(IF_ICMPEQ, valid)
                visitVarInsn(ILOAD, 1); visitInsn(ICONST_3)
                visitFieldInsn(GETSTATIC, "FixtureSerializer", "descriptor", "Lkotlinx/serialization/descriptors/SerialDescriptor;")
                visitMethodInsn(INVOKESTATIC, "kotlinx/serialization/internal/PluginExceptionsKt", "throwMissingFieldException", "(IILkotlinx/serialization/descriptors/SerialDescriptor;)V", false)
                visitLabel(valid); visitVarInsn(ALOAD, 0); visitMethodInsn(INVOKESPECIAL, "java/lang/Object", "<init>", "()V", false)
                visitVarInsn(ALOAD, 0); visitVarInsn(ALOAD, 2); visitFieldInsn(PUTFIELD, "FixturePages", "base", "Ljava/lang/String;")
                visitVarInsn(ALOAD, 0); visitVarInsn(ALOAD, 3); visitFieldInsn(PUTFIELD, "FixturePages", "items", "Ljava/util/List;")
                visitInsn(RETURN); visitMaxs(0, 0); visitEnd()
            }
        }
        type("FixtureSerializer") { writer ->
            writer.visitField(ACC_PUBLIC or ACC_STATIC, "descriptor", "Lkotlinx/serialization/descriptors/SerialDescriptor;", null, null).visitEnd()
            writer.visitMethod(ACC_STATIC, "<clinit>", "()V", null, null).apply {
                visitCode(); visitTypeInsn(NEW, "kotlinx/serialization/internal/PluginGeneratedSerialDescriptor"); visitInsn(DUP)
                visitLdcInsn("eu.kanade.tachiyomi.extension.en.comix.ChapterResponse.Pages"); visitInsn(ACONST_NULL); visitInsn(ICONST_2)
                visitMethodInsn(INVOKESPECIAL, "kotlinx/serialization/internal/PluginGeneratedSerialDescriptor", "<init>", "(Ljava/lang/String;Lkotlinx/serialization/internal/GeneratedSerializer;I)V", false)
                for (name in listOf("baseUrl", "items")) { visitInsn(DUP); visitLdcInsn(name); visitInsn(ICONST_0); visitMethodInsn(INVOKEVIRTUAL, "kotlinx/serialization/internal/PluginGeneratedSerialDescriptor", "addElement", "(Ljava/lang/String;Z)V", false) }
                visitFieldInsn(PUTSTATIC, "FixtureSerializer", "descriptor", "Lkotlinx/serialization/descriptors/SerialDescriptor;")
                visitInsn(RETURN); visitMaxs(0, 0); visitEnd()
            }
            writer.visitMethod(ACC_PUBLIC or ACC_STATIC, "construct", "()V", null, null).apply {
                visitCode(); visitTypeInsn(NEW, "FixturePages"); visitInsn(DUP); visitInsn(ICONST_3); visitLdcInsn(""); visitInsn(ACONST_NULL)
                visitMethodInsn(INVOKESPECIAL, "FixturePages", "<init>", "(ILjava/lang/String;Ljava/util/List;)V", false)
                visitInsn(POP); visitInsn(RETURN); visitMaxs(0, 0); visitEnd()
            }
        }
        type("FixtureItemSerializer") { writer ->
            writer.visitMethod(ACC_PUBLIC or ACC_STATIC, "construct", "()V", null, null).apply {
                visitCode(); visitLdcInsn("eu.kanade.tachiyomi.extension.en.comix.ChapterResponse.PageDto"); visitInsn(POP)
                visitTypeInsn(NEW, "FixtureItem"); visitInsn(DUP); visitInsn(ICONST_1); visitLdcInsn(""); visitInsn(ICONST_0)
                visitMethodInsn(INVOKESPECIAL, "FixtureItem", "<init>", "(ILjava/lang/String;I)V", false)
                visitInsn(POP); visitInsn(RETURN); visitMaxs(0, 0); visitEnd()
            }
        }
        JarOutputStream(Files.newOutputStream(jar)).use { out ->
            classes.forEach { (name, bytes) -> out.putNextEntry(JarEntry("$name.class")); out.write(bytes); out.closeEntry() }
        }
    }

    @Test
    fun `missing base permits only absolute http image URLs`() {
        ComixCompatibility.requireAbsoluteImageUrl("https://images.example/one.jpg?token=fixture")
        ComixCompatibility.requireAbsoluteImageUrl("http://images.example/two.jpg")
        for (url in listOf("one.jpg", "/one.jpg", "//images.example/one.jpg", "httpbad", "https:///one.jpg", "ftp://images.example/one.jpg", "https://images.example/bad path")) {
            assertFailsWith<IllegalArgumentException>(url) { ComixCompatibility.requireAbsoluteImageUrl(url) }
        }
    }

    @Test
    fun `other packages and versions remain byte identical`() {
        val root = createTempDirectory("comix-scope")
        try {
            val jar = root.resolve("fixture.jar")
            JarOutputStream(Files.newOutputStream(jar)).use { out ->
                out.putNextEntry(JarEntry("unrelated.txt"))
                out.write("preserved".toByteArray())
            }
            val original = Files.readAllBytes(jar)
            ComixCompatibility.apply(jar, "other", "1.6.42", 106042)
            ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.43", 106043)
            assertContentEquals(original, Files.readAllBytes(jar))
            assertFailsWith<IllegalArgumentException> {
                ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.42", 106042)
            }
            assertContentEquals(original, Files.readAllBytes(jar))
        } finally {
            root.toFile().deleteRecursively()
        }
    }
}
