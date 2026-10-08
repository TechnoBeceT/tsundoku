package enginehost

import org.objectweb.asm.ClassReader
import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Opcodes
import org.objectweb.asm.tree.ClassNode
import org.objectweb.asm.tree.LdcInsnNode
import org.objectweb.asm.tree.MethodInsnNode
import java.io.IOException
import java.nio.file.FileSystems
import java.nio.file.Files
import java.nio.file.Path

/**
 * The Blank 1.6.1 expects an object export table; the reader now ships shuffled pairs.
 * Adapt only fetched asset text in the verified release's derived jar. Its WASM import
 * checks, unmasking, signing, chapter authorization and image decryption remain intact.
 */
internal object BlankCompatibility {
    private const val PACKAGE = "eu.kanade.tachiyomi.extension.en.theblank"
    private const val SIGNER = "9add655a78e96c4ec7a53ef89dccb557cb5d767489fac5e785d671a5a75d4da2"
    private val required = setOf("freeBuffer", "ecdhInit", "signAttestation", "signManifest", "kdfRot")
    private val pair = Regex("""\["([\w$]+)","(_[\w$]+)"\]""")
    private val table = Regex("""\[(\["[\w$]+","_[\w$]+"\](?:,\["[\w$]+","_[\w$]+"\])*)\]\.reduce\(""")

    fun apply(jar: Path, packageName: String, versionName: String, versionCode: Long, signers: Set<String>) {
        if (packageName != PACKAGE || versionName != "1.6.1" || versionCode != 106001L) return
        require(signers == setOf(SIGNER)) { "The Blank 1.6.1 compatibility: unexpected APK signer" }
        FileSystems.newFileSystem(jar).use { zip ->
            val entries = Files.walk(zip.getPath("/")).use { paths ->
                paths.filter { it.toString().endsWith(".class") }.toList().associateWith { path ->
                    ClassNode().also { ClassReader(Files.readAllBytes(path)).accept(it, 0) }
                }
            }
            val reader = entries.entries.singleOrNull { (_, node) ->
                node.methods.any { method -> method.instructions.toArray().any { it is LdcInsnNode && it.cst == "Reader signer bindings not found" } }
            } ?: throw IllegalArgumentException("The Blank 1.6.1 compatibility: unexpected reader module")
            val asset = reader.value.methods.singleOrNull { method ->
                method.desc == "(Lokhttp3/OkHttpClient;Ljava/lang/String;Lokhttp3/Headers;Ljava/lang/String;Lkotlin/coroutines/jvm/internal/ContinuationImpl;)Ljava/lang/Object;" &&
                    method.instructions.toArray().any { it is LdcInsnNode && it.cst == "/build/assets/" }
            } ?: throw IllegalArgumentException("The Blank 1.6.1 compatibility: unexpected asset loader")
            val body = asset.instructions.toArray().filterIsInstance<MethodInsnNode>().singleOrNull {
                it.owner == "okhttp3/ResponseBody" && it.name == "string" && it.desc == "()Ljava/lang/String;"
            } ?: throw IllegalArgumentException("The Blank 1.6.1 compatibility: unexpected asset response")
            require(asset.instructions.toArray().none { it is MethodInsnNode && it.owner == "enginehost/BlankCompatibility" }) {
                "The Blank 1.6.1 compatibility: reader already adapted"
            }
            asset.instructions.insert(body, MethodInsnNode(Opcodes.INVOKESTATIC, "enginehost/BlankCompatibility", "normalizeBindings", "(Ljava/lang/String;)Ljava/lang/String;", false))
            val writer = ClassWriter(ClassWriter.COMPUTE_MAXS)
            reader.value.accept(writer)
            Files.write(reader.key, writer.toByteArray())
        }
    }

    /** Converts a complete reader table to the original parser's object form, without running JS. */
    @JvmStatic
    fun normalizeBindings(script: String): String {
        if (!script.contains("[\"freeBuffer\"")) return script
        val candidates = table.findAll(script).filter { match -> pair.findAll(match.groupValues[1]).any { it.groupValues[1] == "freeBuffer" } }.toList()
        if (candidates.size != 1) throw IOException("Unsupported reader export table")
        val candidate = candidates.single()
        val bindings = pair.findAll(candidate.groupValues[1]).map { it.groupValues[1] to it.groupValues[2] }.toList()
        if (bindings.map { it.first }.distinct().size != bindings.size || bindings.map { it.second }.distinct().size != bindings.size ||
            !bindings.map { it.first }.containsAll(required)) throw IOException("Unsupported reader export table")
        // freeBuffer must lead the object because the released parser uses it as its anchor.
        val ordered = bindings.sortedBy { if (it.first == "freeBuffer") 0 else 1 }
        val objectTable = ordered.joinToString(",", "{", "}") { (name, export) -> "$name:\"$export\"" }
        return script.replaceRange(candidate.range, "$objectTable.reduce(")
    }
}
