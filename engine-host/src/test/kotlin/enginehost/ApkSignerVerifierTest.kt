package enginehost

import com.android.apksig.ApkSigner
import java.nio.file.Files
import java.nio.file.Path
import java.security.KeyStore
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.cert.X509Certificate
import java.util.Base64
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream
import kotlin.io.path.readBytes
import kotlin.io.path.writeBytes
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class ApkSignerVerifierTest {
    @Test
    fun `cryptographically valid APK returns its normalized certificate fingerprint`() {
        val fixture = SignedApkFixture()

        val fingerprints = ApkSignerVerifier.verify(fixture.signedApk)

        assertEquals(setOf(fixture.fingerprint), fingerprints)
    }

    @Test
    fun `APK modified after signing is rejected`() {
        val fixture = SignedApkFixture()
        val tampered = fixture.signedApk.resolveSibling("tampered.apk")
        val bytes = fixture.signedApk.readBytes()
        bytes[bytes.size / 3] = (bytes[bytes.size / 3].toInt() xor 1).toByte()
        tampered.writeBytes(bytes)

        val failure = assertFailsWith<IllegalArgumentException> { ApkSignerVerifier.verify(tampered) }

        assertTrue(failure.message.orEmpty().contains("signature verification failed"))
    }

    @Test
    fun `unsigned APK is rejected`() {
        val fixture = SignedApkFixture()

        val failure = assertFailsWith<IllegalArgumentException> { ApkSignerVerifier.verify(fixture.unsignedApk) }

        assertTrue(failure.message.orEmpty().contains("signature verification failed"))
    }

    @Test
    fun `extension preparation rejects an unsigned direct URL artifact before reading metadata`() {
        val fixture = SignedApkFixture()
        val loader = ExtensionLoader(Files.createTempDirectory("unsigned-extension-prepare").toFile())

        val failure = assertFailsWith<IllegalArgumentException> { loader.prepareFromApk(fixture.unsignedApk) }

        assertTrue(failure.message.orEmpty().contains("signature verification failed"))
    }
}

internal class SignedApkFixture {
    private val root = Files.createTempDirectory("apk-signer-verifier")
    val unsignedApk: Path = root.resolve("unsigned.apk")
    val signedApk: Path = root.resolve("signed.apk")
    val fingerprint: String

    init {
        ZipOutputStream(Files.newOutputStream(unsignedApk)).use { apk ->
            apk.putNextEntry(ZipEntry("AndroidManifest.xml"))
            apk.write(Base64.getDecoder().decode(BINARY_MANIFEST))
            apk.closeEntry()
            apk.putNextEntry(ZipEntry("classes.dex"))
            apk.write("test dex payload".toByteArray())
            apk.closeEntry()
        }

        val keyStorePath = root.resolve("signing.p12")
        val password = "test-password".toCharArray()
        val keytool = Path.of(System.getProperty("java.home"), "bin", "keytool").toString()
        val process =
            ProcessBuilder(
                keytool,
                "-genkeypair",
                "-alias",
                "test",
                "-keystore",
                keyStorePath.toString(),
                "-storetype",
                "PKCS12",
                "-storepass",
                String(password),
                "-keypass",
                String(password),
                "-keyalg",
                "RSA",
                "-keysize",
                "2048",
                "-validity",
                "1",
                "-dname",
                "CN=Ephemeral Test Signer",
                "-noprompt",
            ).redirectErrorStream(true)
                .start()
        val output = process.inputStream.bufferedReader().use { it.readText() }
        check(process.waitFor() == 0) { "keytool failed: $output" }

        val keyStore = KeyStore.getInstance("PKCS12")
        Files.newInputStream(keyStorePath).use { keyStore.load(it, password) }
        val privateKey = keyStore.getKey("test", password) as PrivateKey
        val certificate = keyStore.getCertificate("test") as X509Certificate
        fingerprint = MessageDigest.getInstance("SHA-256").digest(certificate.encoded).toHex()

        @Suppress("DEPRECATION")
        val signerConfig = ApkSigner.SignerConfig.Builder("test", privateKey, listOf(certificate)).build()
        ApkSigner.Builder(listOf(signerConfig))
            .setInputApk(unsignedApk.toFile())
            .setOutputApk(signedApk.toFile())
            .setMinSdkVersion(24)
            .setV1SigningEnabled(false)
            .setV2SigningEnabled(true)
            .setV3SigningEnabled(false)
            .setV4SigningEnabled(false)
            .build()
            .sign()
    }

    private fun ByteArray.toHex(): String = joinToString("") { byte -> "%02x".format(byte) }

    companion object {
        // Minimal aapt2-compiled manifest for package test.extension, minSdk 24.
        private const val BINARY_MANIFEST =
            "AwAIAJADAAABABwAGAIAAA0AAAAAAAAAAAAAAFAAAAAAAAAAAAAAAB4AAABEAAAAegAAAIIAAACUAAAArgAAAAYBAAAaAQAALAEAAGABAACUAQAAtAEAAA0AbQBpAG4AUwBkAGsAVgBlAHIAcwBpAG8AbgAAABEAYwBvAG0AcABpAGwAZQBTAGQAawBWAGUAcgBzAGkAbwBuAAAAGQBjAG8AbQBwAGkAbABlAFMAZABrAFYAZQByAHMAaQBvAG4AQwBvAGQAZQBuAGEAbQBlAAAAAgAxADEAAAAHAGEAbgBkAHIAbwBpAGQAAAALAGEAcABwAGwAaQBjAGEAdABpAG8AbgAAACoAaAB0AHQAcAA6AC8ALwBzAGMAaABlAG0AYQBzAC4AYQBuAGQAcgBvAGkAZAAuAGMAbwBtAC8AYQBwAGsALwByAGUAcwAvAGEAbgBkAHIAbwBpAGQAAAAIAG0AYQBuAGkAZgBlAHMAdAAAAAcAcABhAGMAawBhAGcAZQAAABgAcABsAGEAdABmAG8AcgBtAEIAdQBpAGwAZABWAGUAcgBzAGkAbwBuAEMAbwBkAGUAAAAYAHAAbABhAHQAZgBvAHIAbQBCAHUAaQBsAGQAVgBlAHIAcwBpAG8AbgBOAGEAbQBlAAAADgB0AGUAcwB0AC4AZQB4AHQAZQBuAHMAaQBvAG4AAAAIAHUAcwBlAHMALQBzAGQAawAAAIABCAAUAAAADAIBAXIFAQFzBQEBAAEQABgAAAABAAAA/////wQAAAAGAAAAAgEQAIgAAAABAAAA//////////8HAAAAFAAUAAUAAAAAAAAABgAAAAEAAAD/////CAAAEB4AAAAGAAAAAgAAAAMAAAAIAAADAwAAAP////8IAAAACwAAAAgAAAMLAAAA/////wkAAAD/////CAAAEB4AAAD/////CgAAAP////8IAAAQCwAAAAIBEAA4AAAAAgAAAP//////////DAAAABQAFAABAAAAAAAAAAYAAAAAAAAA/////wgAABAYAAAAAwEQABgAAAACAAAA//////////8MAAAAAgEQACQAAAADAAAA//////////8FAAAAFAAUAAAAAAAAAAAAAwEQABgAAAADAAAA//////////8FAAAAAwEQABgAAAABAAAA//////////8HAAAAAQEQABgAAAABAAAA/////wQAAAAGAAAA"
    }
}
