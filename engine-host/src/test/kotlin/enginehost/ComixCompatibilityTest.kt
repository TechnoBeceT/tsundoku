package enginehost

import org.objectweb.asm.ClassReader
import org.objectweb.asm.ClassWriter
import org.objectweb.asm.tree.ClassNode
import org.objectweb.asm.tree.LdcInsnNode
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

    @Test
    fun `released chapter bootstrap supports renamed bundles for both official versions`() {
        for (version in listOf(42, 43, 44)) {
            val root = createTempDirectory("comix-chapter-script")
            try {
                val jar = root.resolve("fixture.jar")
                if (version == 42) fixture(jar) else JarOutputStream(Files.newOutputStream(jar)).use { }
                addChapterBootstrap(jar, releasedChapterTemplate(version))
                ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.$version", 106000L + version)
                val corrected = chapterTemplate(jar)
                assertTrue(!corrected.contains("env-["))
                assertTrue(corrected.contains("Object.values(environment)"))
                assertTrue(corrected.contains("url.origin !== mainUrl.origin"))
                assertTrue(corrected.contains("bundleFiles.length > 32"))
                assertEquals(5, corrected.count { it == '\u0001' })
                assertEquals(releasedChapterTemplate(version).substringAfter("const items = [];"), corrected.substringAfter("const items = [];"))
                assertEquals(releasedChapterTemplate(version).substringBefore("if (!mainScriptUrl)"), corrected.substringBefore("if (!mainScriptUrl)"))
            } finally {
                root.toFile().deleteRecursively()
            }
        }
    }

    @Test
    fun `released callback discovers signed raw client and route without obfuscated DTOs`() {
        // GraalJS already ships on the engine runtime classpath. Reflection avoids introducing
        // another compile dependency for this existing consumer's JavaScript regression.
        val contextClass = Class.forName("org.graalvm.polyglot.Context")
        val valueClass = Class.forName("org.graalvm.polyglot.Value")
        for (version in listOf(42, 43, 44)) {
            val root = createTempDirectory("comix-discovery-semantics")
            try {
                val jar = root.resolve("fixture.jar")
                if (version == 42) fixture(jar) else JarOutputStream(Files.newOutputStream(jar)).use { }
                addChapterBootstrap(jar, releasedChapterTemplate(version))
                ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.$version", 106000L + version)
                val slots = ArrayDeque(listOf("\"xkry7\"", "\"https://comix.to/dist/main.js\"", "null", "pass", "reject"))
                val script = chapterTemplate(jar).split('\u0001').joinToString("") { fragment ->
                    fragment + if (slots.isEmpty()) "" else slots.removeFirst()
                }
                val context = contextClass.getMethod("create", Array<String>::class.java).invoke(null, arrayOf("js"))
                try {
                    val bindings = contextClass.getMethod("getBindings", String::class.java).invoke(context, "js")
                    valueClass.getMethod("putMember", String::class.java, Any::class.java).invoke(bindings, "callbackScript", script)
                    val eval = contextClass.getMethod("eval", String::class.java, CharSequence::class.java)
                    eval.invoke(context, "js", javaClass.getResource("/comix-chapter-discovery.js")!!.readText())
                    val result = eval.invoke(context, "js", "discoveryResult")
                    assertEquals("PASS", valueClass.getMethod("asString").invoke(result), "official $version")
                } finally {
                    contextClass.getMethod("close").invoke(context)
                }
            } finally {
                root.toFile().deleteRecursively()
            }
        }
    }

    @Test
    fun `released chapter callbacks reject a terminal captcha without waiting for site challenge`() {
        for (version in listOf(44, 42, 43)) assertCaptchaResponse(version, false)
    }

    @Test
    fun `released page callback rejects a terminal captcha without waiting for site challenge`() =
        assertCaptchaResponse(44, true)

    private fun assertCaptchaResponse(version: Int, pages: Boolean) {
        val contextClass = Class.forName("org.graalvm.polyglot.Context")
        val valueClass = Class.forName("org.graalvm.polyglot.Value")
        val root = createTempDirectory("comix-captcha-response")
        try {
            val jar = root.resolve("fixture.jar")
            if (version == 42) fixture(jar) else JarOutputStream(Files.newOutputStream(jar)).use { }
            addChapterBootstrap(jar, releasedChapterTemplate(version))
            ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.$version", 106000L + version)
            val template = if (pages) java.nio.file.FileSystems.newFileSystem(jar).use { zip ->
                val node = ClassNode().also { ClassReader(Files.readAllBytes(zip.getPath("/PageBootstrap.class"))).accept(it, 0) }
                node.methods.single().instructions.toArray().filterIsInstance<LdcInsnNode>().joinToString("\u0001") { it.cst as String }
            } else chapterTemplate(jar)
            val slots = ArrayDeque(if (pages) listOf("pass", "reject") else listOf("\"xkry7\"", "\"https://comix.to/dist/main.js\"", "null", "pass", "reject"))
            val script = template.split('\u0001').joinToString("") { fragment -> fragment + if (slots.isEmpty()) "" else slots.removeFirst() }
            val context = contextClass.getMethod("create", Array<String>::class.java).invoke(null, arrayOf("js"))
            try {
                val bindings = contextClass.getMethod("getBindings", String::class.java).invoke(context, "js")
                val put = valueClass.getMethod("putMember", String::class.java, Any::class.java)
                put.invoke(bindings, "callbackScript", script)
                put.invoke(bindings, "pageCallback", pages)
                val eval = contextClass.getMethod("eval", String::class.java, CharSequence::class.java)
                eval.invoke(context, "js", javaClass.getResource("/comix-captcha-response.js")!!.readText())
                assertEquals("PASS", valueClass.getMethod("asString").invoke(eval.invoke(context, "js", "discoveryResult")), "official $version pages=$pages")
            } finally { contextClass.getMethod("close").invoke(context) }
        } finally { root.toFile().deleteRecursively() }
    }

    @Test
    fun `official 44 page callback uses signed raw response instead of parse observation`() {
        val root = createTempDirectory("comix-page-discovery")
        try {
            val jar = root.resolve("fixture.jar")
            JarOutputStream(Files.newOutputStream(jar)).use { }
            addChapterBootstrap(jar, releasedChapterTemplate(44))
            ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.44", 106044)
            val node = java.nio.file.FileSystems.newFileSystem(jar).use { zip ->
                ClassNode().also { ClassReader(Files.readAllBytes(zip.getPath("/PageBootstrap.class"))).accept(it, 0) }
            }
            val fragments = node.methods.single().instructions.toArray().filterIsInstance<LdcInsnNode>().map { it.cst as String }
            val slots = ArrayDeque(listOf("pass", "reject"))
            val script = fragments.mapIndexed { index, text -> text + if (index == fragments.lastIndex) "" else slots.removeFirst() }.joinToString("")
            val contextClass = Class.forName("org.graalvm.polyglot.Context")
            val valueClass = Class.forName("org.graalvm.polyglot.Value")
            val context = contextClass.getMethod("create", Array<String>::class.java).invoke(null, arrayOf("js"))
            try {
                val bindings = contextClass.getMethod("getBindings", String::class.java).invoke(context, "js")
                valueClass.getMethod("putMember", String::class.java, Any::class.java).invoke(bindings, "callbackScript", script)
                val eval = contextClass.getMethod("eval", String::class.java, CharSequence::class.java)
                eval.invoke(context, "js", javaClass.getResource("/comix-page-discovery.js")!!.readText())
                val result = eval.invoke(context, "js", "discoveryResult")
                assertEquals("PASS", valueClass.getMethod("asString").invoke(result))
            } finally { contextClass.getMethod("close").invoke(context) }
        } finally { root.toFile().deleteRecursively() }
    }

    @Test
    fun `unexpected released bootstrap refuses without changing jar contents`() {
        for (version in listOf(43, 44)) {
            val root = createTempDirectory("comix-chapter-shape")
            try {
                val jar = root.resolve("fixture.jar")
                JarOutputStream(Files.newOutputStream(jar)).use { }
                addChapterBootstrap(jar, releasedChapterTemplate(version).replace("page <= 200", "page <= 201"))
                val original = Files.readAllBytes(jar)
                assertFailsWith<IllegalArgumentException> {
                    ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.$version", 106000L + version)
                }
                assertContentEquals(original, Files.readAllBytes(jar))
            } finally {
                root.toFile().deleteRecursively()
            }
        }
    }

    @Test
    fun `unexpected official 44 page bootstrap refuses before writing either callback`() {
        val root = createTempDirectory("comix-page-shape")
        try {
            val jar = root.resolve("fixture.jar")
            JarOutputStream(Files.newOutputStream(jar)).use { }
            addChapterBootstrap(jar, releasedChapterTemplate(44))
            addPageBootstrap(jar, javaClass.getResource("/comix-page-template44.txt")!!.readText().replace("return false;", "return true;"))
            val original = Files.readAllBytes(jar)
            assertFailsWith<IllegalArgumentException> {
                ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.44", 106044)
            }
            assertContentEquals(original, Files.readAllBytes(jar))
        } finally { root.toFile().deleteRecursively() }
    }

    private fun releasedChapterTemplate(version: Int = 42) = javaClass.getResource(if (version == 44) "/comix-chapter-template44.txt" else "/comix-chapter-template.txt")!!.readText()

    private fun addChapterBootstrap(jar: java.nio.file.Path, template: String) {
        val writer = ClassWriter(0)
        writer.visit(V11, ACC_PUBLIC, "ChapterBootstrap", null, "java/lang/Object", null)
        writer.visitMethod(ACC_PUBLIC, "invoke", "(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;", null, null).apply {
            visitCode()
            val fragments = template.split('\u0001')
            visitTypeInsn(NEW, "java/lang/StringBuilder")
            visitInsn(DUP)
            visitLdcInsn(fragments.first())
            visitMethodInsn(INVOKESPECIAL, "java/lang/StringBuilder", "<init>", "(Ljava/lang/String;)V", false)
            for (fragment in fragments.drop(1)) {
                visitVarInsn(ALOAD, 1)
                visitTypeInsn(CHECKCAST, "java/lang/String")
                visitMethodInsn(INVOKEVIRTUAL, "java/lang/StringBuilder", "append", "(Ljava/lang/String;)Ljava/lang/StringBuilder;", false)
                visitLdcInsn(fragment)
                visitMethodInsn(INVOKEVIRTUAL, "java/lang/StringBuilder", "append", "(Ljava/lang/String;)Ljava/lang/StringBuilder;", false)
            }
            visitMethodInsn(INVOKEVIRTUAL, "java/lang/StringBuilder", "toString", "()Ljava/lang/String;", false)
            visitInsn(ARETURN)
            visitMaxs(3, 3)
            visitEnd()
        }
        writer.visitEnd()
        java.nio.file.FileSystems.newFileSystem(jar).use { zip -> Files.write(zip.getPath("/ChapterBootstrap.class"), writer.toByteArray()) }
        if (template.contains("const bundleFiles = Array.from(")) addPageBootstrap(jar)
    }

    private fun addPageBootstrap(jar: java.nio.file.Path, template: String = javaClass.getResource("/comix-page-template44.txt")!!.readText()) {
        val fragments = template.split('\u0001')
        val writer = ClassWriter(0)
        writer.visit(V11, ACC_PUBLIC, "PageBootstrap", null, "java/lang/Object", null)
        writer.visitMethod(ACC_PUBLIC, "invoke", "(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;", null, null).apply {
            visitCode()
            visitTypeInsn(NEW, "java/lang/StringBuilder"); visitInsn(DUP); visitLdcInsn(fragments[0])
            visitMethodInsn(INVOKESPECIAL, "java/lang/StringBuilder", "<init>", "(Ljava/lang/String;)V", false)
            visitVarInsn(ASTORE, 2)
            visitVarInsn(ALOAD, 2); visitVarInsn(ALOAD, 1); visitTypeInsn(CHECKCAST, "java/lang/String")
            visitMethodInsn(INVOKEVIRTUAL, "java/lang/StringBuilder", "append", "(Ljava/lang/String;)Ljava/lang/StringBuilder;", false); visitInsn(POP)
            visitVarInsn(ALOAD, 2); visitLdcInsn(fragments[1])
            visitMethodInsn(INVOKEVIRTUAL, "java/lang/StringBuilder", "append", "(Ljava/lang/String;)Ljava/lang/StringBuilder;", false); visitInsn(POP)
            visitVarInsn(ALOAD, 2); visitMethodInsn(INVOKEVIRTUAL, "java/lang/StringBuilder", "toString", "()Ljava/lang/String;", false)
            visitInsn(ARETURN); visitMaxs(3, 4); visitEnd()
        }
        writer.visitEnd()
        java.nio.file.FileSystems.newFileSystem(jar).use { zip -> Files.write(zip.getPath("/PageBootstrap.class"), writer.toByteArray()) }
    }

    private fun chapterTemplate(jar: java.nio.file.Path): String = java.nio.file.FileSystems.newFileSystem(jar).use { zip ->
        val node = ClassNode().also { ClassReader(Files.readAllBytes(zip.getPath("/ChapterBootstrap.class"))).accept(it, 0) }
        node.methods.flatMap { it.instructions.toArray().toList() }.filterIsInstance<LdcInsnNode>().map { it.cst as String }.joinToString("\u0001")
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
        addChapterBootstrap(jar, releasedChapterTemplate())
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
            ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.45", 106045)
            ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.44", 106043)
            ComixCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.comix", "1.6.43", 106044)
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
