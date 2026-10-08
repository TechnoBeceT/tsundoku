package enginehost

import org.objectweb.asm.ClassReader
import org.objectweb.asm.ClassWriter
import org.objectweb.asm.Opcodes
import org.objectweb.asm.tree.ClassNode
import org.objectweb.asm.tree.FieldInsnNode
import org.objectweb.asm.tree.InsnList
import org.objectweb.asm.tree.InsnNode
import org.objectweb.asm.tree.JumpInsnNode
import org.objectweb.asm.tree.LabelNode
import org.objectweb.asm.tree.LdcInsnNode
import org.objectweb.asm.tree.MethodInsnNode
import org.objectweb.asm.tree.TypeInsnNode
import org.objectweb.asm.tree.VarInsnNode
import java.net.URI
import java.net.URISyntaxException
import java.nio.file.FileSystems
import java.nio.file.Files
import java.nio.file.Path

/**
 * Comix 1.6.42 requires a base URL even when the site returns only absolute page URLs.
 * Correct only its verified APK's derived jar; package/version/signing identity stay official.
 * Serialization names and constructor shapes identify the DTOs without depending on R8 names.
 */
internal object ComixCompatibility {
    fun apply(jar: Path, packageName: String, versionName: String, versionCode: Long) {
        if (packageName != "eu.kanade.tachiyomi.extension.en.comix" || versionName != "1.6.42" || versionCode != 106042L) return
        FileSystems.newFileSystem(jar).use { zip ->
            val nodes = Files.walk(zip.getPath("/")).use { paths ->
                paths.filter { it.toString().endsWith(".class") }.toList().associateWith { read(Files.readAllBytes(it)) }
            }
            fun serializerFor(serialName: String): Map.Entry<Path, ClassNode> = nodes.entries.singleOrNull { (_, node) ->
                node.methods.any { method -> method.instructions.toArray().any { it is LdcInsnNode && it.cst == serialName } }
            } ?: throw IllegalArgumentException("Comix 1.6.42 compatibility: unexpected serializer $serialName")
            val serializerEntry = serializerFor("eu.kanade.tachiyomi.extension.en.comix.ChapterResponse.Pages")
            val serializer = serializerEntry.value
            val constructorCall = serializer.methods.flatMap { it.instructions.toArray().toList() }.filterIsInstance<MethodInsnNode>()
                .singleOrNull { it.name == "<init>" && it.desc == "(ILjava/lang/String;Ljava/util/List;)V" }
            require(constructorCall != null) { "Comix 1.6.42 compatibility: unexpected Pages constructor call" }
            val pagesEntry = nodes.entries.single { it.value.name == constructorCall.owner }
            val pages = pagesEntry.value
            val ctor = pages.methods.singleOrNull { it.name == "<init>" && it.desc == constructorCall.desc }
            require(ctor != null && pages.fields.count { it.desc == "Ljava/lang/String;" } == 1 && pages.fields.count { it.desc == "Ljava/util/List;" } == 1) {
                "Comix 1.6.42 compatibility: unexpected Pages constructor"
            }
            val itemSerializer = serializerFor("eu.kanade.tachiyomi.extension.en.comix.ChapterResponse.PageDto").value
            val itemCall = itemSerializer.methods.flatMap { it.instructions.toArray().toList() }.filterIsInstance<MethodInsnNode>()
                .singleOrNull { it.name == "<init>" && it.desc == "(ILjava/lang/String;I)V" }
            require(itemCall != null) { "Comix 1.6.42 compatibility: unexpected image constructor" }
            val item = nodes.values.single { it.name == itemCall.owner }
            val urlField = item.fields.singleOrNull { it.desc == "Ljava/lang/String;" && it.access and Opcodes.ACC_PUBLIC != 0 }
            require(urlField != null) { "Comix 1.6.42 compatibility: unexpected image URL field" }
            val instructions = ctor.instructions.toArray()
            val executable = instructions.filter { it.opcode >= 0 }
            require(executable.take(5).map { it.opcode } == listOf(Opcodes.ICONST_3, Opcodes.ILOAD, Opcodes.ICONST_3, Opcodes.IAND, Opcodes.IF_ICMPEQ) && (executable[1] as VarInsnNode).`var` == 1) {
                "Comix 1.6.42 compatibility: unexpected required-field mask"
            }
            require(instructions.count { it is MethodInsnNode && it.owner == "kotlinx/serialization/internal/PluginExceptionsKt" && it.name == "throwMissingFieldException" } == 1) {
                "Comix 1.6.42 compatibility: unexpected required-field validation"
            }
            val clinit = serializer.methods.single { it.name == "<clinit>" }
            val descriptor = clinit.instructions.toArray()
            require(descriptor.any { it is LdcInsnNode && it.cst == "eu.kanade.tachiyomi.extension.en.comix.ChapterResponse.Pages" }) {
                "Comix 1.6.42 compatibility: unexpected page serializer"
            }
            val elements = descriptor.filterIsInstance<MethodInsnNode>().filter { it.owner == "kotlinx/serialization/internal/PluginGeneratedSerialDescriptor" && it.name == "addElement" }
            require(elements.size == 2 && elements.all { it.desc == "(Ljava/lang/String;Z)V" && it.previous.opcode == Opcodes.ICONST_0 } &&
                (elements[0].previous.previous as? LdcInsnNode)?.cst == "baseUrl" && (elements[1].previous.previous as? LdcInsnNode)?.cst == "items") {
                "Comix 1.6.42 compatibility: unexpected required page fields"
            }
            val baseName = descriptor.singleOrNull { it is LdcInsnNode && it.cst == "baseUrl" }
            require(baseName != null && baseName.next.opcode == Opcodes.ICONST_0 && baseName.next.next is MethodInsnNode && (baseName.next.next as MethodInsnNode).name == "addElement") {
                "Comix 1.6.42 compatibility: unexpected base URL descriptor"
            }
            // Validate every URL before defaulting an absent base. Keep the original required-items
            // check and all original page ordering, scrambling and image-request code unchanged.
            ctor.instructions.insert(missingBaseGuard(item.name, urlField.name))
            clinit.instructions.set(baseName.next, InsnNode(Opcodes.ICONST_1))
            val pageBytes = write(pages)
            val serializerBytes = write(serializer)
            Files.write(pagesEntry.key, pageBytes)
            Files.write(serializerEntry.key, serializerBytes)
        }
    }

    /** Called by the transformed constructor before accepting a page without a base URL. */
    @JvmStatic
    fun requireAbsoluteImageUrl(url: String) {
        val uri = try { URI(url) } catch (_: URISyntaxException) {
            throw IllegalArgumentException("Comix page without baseUrl requires an absolute HTTP(S) image URL")
        }
        require((uri.scheme == "http" || uri.scheme == "https") && !uri.host.isNullOrEmpty()) {
            "Comix page without baseUrl requires an absolute HTTP(S) image URL"
        }
    }

    private fun missingBaseGuard(itemName: String, urlField: String): InsnList {
        val code = InsnList()
        val done = LabelNode()
        val loop = LabelNode()
        val defaultBase = LabelNode()
        code.add(VarInsnNode(Opcodes.ILOAD, 1))
        code.add(InsnNode(Opcodes.ICONST_1))
        code.add(InsnNode(Opcodes.IAND))
        code.add(JumpInsnNode(Opcodes.IFNE, done))
        // A missing items field must reach the original MissingFieldException, rather than NPE.
        code.add(VarInsnNode(Opcodes.ILOAD, 1))
        code.add(InsnNode(Opcodes.ICONST_2))
        code.add(InsnNode(Opcodes.IAND))
        code.add(JumpInsnNode(Opcodes.IFEQ, done))
        code.add(VarInsnNode(Opcodes.ALOAD, 3))
        code.add(MethodInsnNode(Opcodes.INVOKEINTERFACE, "java/util/List", "iterator", "()Ljava/util/Iterator;", true))
        code.add(VarInsnNode(Opcodes.ASTORE, 4))
        code.add(loop)
        code.add(VarInsnNode(Opcodes.ALOAD, 4))
        code.add(MethodInsnNode(Opcodes.INVOKEINTERFACE, "java/util/Iterator", "hasNext", "()Z", true))
        code.add(JumpInsnNode(Opcodes.IFEQ, defaultBase))
        code.add(VarInsnNode(Opcodes.ALOAD, 4))
        code.add(MethodInsnNode(Opcodes.INVOKEINTERFACE, "java/util/Iterator", "next", "()Ljava/lang/Object;", true))
        code.add(TypeInsnNode(Opcodes.CHECKCAST, itemName))
        code.add(FieldInsnNode(Opcodes.GETFIELD, itemName, urlField, "Ljava/lang/String;"))
        code.add(MethodInsnNode(Opcodes.INVOKESTATIC, "enginehost/ComixCompatibility", "requireAbsoluteImageUrl", "(Ljava/lang/String;)V", false))
        code.add(JumpInsnNode(Opcodes.GOTO, loop))
        code.add(defaultBase)
        code.add(LdcInsnNode(""))
        code.add(VarInsnNode(Opcodes.ASTORE, 2))
        code.add(VarInsnNode(Opcodes.ILOAD, 1))
        code.add(InsnNode(Opcodes.ICONST_1))
        code.add(InsnNode(Opcodes.IOR))
        code.add(VarInsnNode(Opcodes.ISTORE, 1))
        code.add(done)
        return code
    }

    private fun read(bytes: ByteArray) = ClassNode().also { ClassReader(bytes).accept(it, 0) }
    private fun write(node: ClassNode): ByteArray = ClassWriter(ClassWriter.COMPUTE_FRAMES or ClassWriter.COMPUTE_MAXS).also { node.accept(it) }.toByteArray()
}
