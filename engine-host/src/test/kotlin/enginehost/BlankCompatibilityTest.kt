package enginehost

import java.io.IOException
import java.net.URLClassLoader
import java.nio.file.Files
import java.nio.file.Path
import java.util.jar.JarEntry
import java.util.jar.JarOutputStream
import okhttp3.ResponseBody.Companion.toResponseBody
import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Opcodes.*
import kotlin.io.path.createTempDirectory
import kotlin.test.assertContentEquals
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class BlankCompatibilityTest {
    private val bindings = listOf(
        "signManifest" to "_rm04b6ia64", "ecdhInit" to "_iq9b6ea3k7",
        "freeBuffer" to "_zur8jthv5y", "signAttestation" to "_gbwb6imt60", "kdfRot" to "_uoyw2jjv5y",
    )
    private fun script(pairs: List<Pair<String, String>> = bindings) =
        "const map=[" + pairs.joinToString(",") { (key, value) -> "[\"$key\",\"$value\"]" } +
            "].reduce((e,[t,n])=>(e[t]=n,e),{});"

    @Test fun `current shuffled pair table yields the legacy semantic map without changing other assets`() {
        val result = BlankCompatibility.normalizeBindings(script())
        assertTrue(result.contains("{freeBuffer:\"_zur8jthv5y\""))
        assertTrue(result.contains("signManifest:\"_rm04b6ia64\""))
        assertEquals("const untouched=42", BlankCompatibility.normalizeBindings("const untouched=42"))
        assertEquals("const old={freeBuffer:\"_old\"}", BlankCompatibility.normalizeBindings("const old={freeBuffer:\"_old\"}"))
    }

    @Test fun `missing duplicated and malformed semantic bindings fail closed`() {
        assertFailsWith<IOException> { BlankCompatibility.normalizeBindings(script(bindings.filter { it.first != "kdfRot" })) }
        assertFailsWith<IOException> { BlankCompatibility.normalizeBindings(script(bindings + bindings.first())) }
        assertFailsWith<IOException> { BlankCompatibility.normalizeBindings(script(bindings + ("other" to bindings.first().second))) }
        assertFailsWith<IOException> { BlankCompatibility.normalizeBindings(script().replace("\"_uoyw2jjv5y\"", "null")) }
        assertFailsWith<IOException> { BlankCompatibility.normalizeBindings(script() + script()) }
    }
    @Test fun `verified release asset response is adapted and other versions are untouched`() {
        val root = createTempDirectory("blank-reader")
        try {
            val jar = root.resolve("fixture.jar")
            fixture(jar)
            val original = Files.readAllBytes(jar)
            BlankCompatibility.apply(jar, "other", "1.6.1", 106001, emptySet())
            BlankCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.theblank", "1.6.2", 106002, emptySet())
            assertContentEquals(original, Files.readAllBytes(jar))
            assertFailsWith<IllegalArgumentException> {
                BlankCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.theblank", "1.6.1", 106001, setOf("unknown"))
            }
            assertContentEquals(original, Files.readAllBytes(jar))
            BlankCompatibility.apply(jar, "eu.kanade.tachiyomi.extension.en.theblank", "1.6.1", 106001,
                setOf("9add655a78e96c4ec7a53ef89dccb557cb5d767489fac5e785d671a5a75d4da2"))
            URLClassLoader(arrayOf(jar.toUri().toURL()), javaClass.classLoader).use { loader ->
                val reader = loader.loadClass("FixtureBlankReader")
                reader.getField("body").set(null, script().toResponseBody())
                val result = reader.methods.single { it.name == "asset" }.invoke(null, null, null, null, null, null) as String
                assertEquals(BlankCompatibility.normalizeBindings(script()), result)
            }
        } finally { root.toFile().deleteRecursively() }
    }

    private fun fixture(jar: Path) {
        val writer = ClassWriter(ClassWriter.COMPUTE_FRAMES or ClassWriter.COMPUTE_MAXS)
        writer.visit(V1_8, ACC_PUBLIC, "FixtureBlankReader", null, "java/lang/Object", null)
        writer.visitField(ACC_PUBLIC or ACC_STATIC, "body", "Lokhttp3/ResponseBody;", null, null).visitEnd()
        writer.visitMethod(ACC_PUBLIC or ACC_STATIC, "marker", "()Ljava/lang/String;", null, null).apply {
            visitCode(); visitLdcInsn("Reader signer bindings not found"); visitInsn(ARETURN); visitMaxs(0, 0); visitEnd()
        }
        writer.visitMethod(ACC_PUBLIC or ACC_STATIC, "asset",
            "(Lokhttp3/OkHttpClient;Ljava/lang/String;Lokhttp3/Headers;Ljava/lang/String;Lkotlin/coroutines/jvm/internal/ContinuationImpl;)Ljava/lang/Object;", null, null).apply {
            visitCode(); visitLdcInsn("/build/assets/"); visitInsn(POP)
            visitFieldInsn(GETSTATIC, "FixtureBlankReader", "body", "Lokhttp3/ResponseBody;")
            visitMethodInsn(INVOKEVIRTUAL, "okhttp3/ResponseBody", "string", "()Ljava/lang/String;", false)
            visitInsn(ARETURN); visitMaxs(0, 0); visitEnd()
        }
        writer.visitEnd()
        JarOutputStream(Files.newOutputStream(jar)).use { output ->
            output.putNextEntry(JarEntry("FixtureBlankReader.class")); output.write(writer.toByteArray()); output.closeEntry()
        }
    }

}
