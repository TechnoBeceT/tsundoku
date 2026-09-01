package enginehost

import com.android.apksig.ApkVerifier
import java.nio.file.Path
import java.security.MessageDigest
import java.util.Locale

internal fun interface ApkSignatureVerifier {
    fun verify(apk: Path): Set<String>
}

/** Cryptographically verify an APK and return its signer certificate SHA-256 fingerprints. */
internal object ApkSignerVerifier : ApkSignatureVerifier {
    override fun verify(apk: Path): Set<String> {
        val result =
            try {
                ApkVerifier.Builder(apk.toFile())
                    .setMinCheckedPlatformVersion(MIN_SUPPORTED_PLATFORM)
                    .build()
                    .verify()
            } catch (failure: Exception) {
                throw IllegalArgumentException("APK signature verification failed for $apk", failure)
            }
        require(result.isVerified) {
            val errors = result.allErrors.joinToString(separator = "; ")
            "APK signature verification failed for $apk${errors.takeIf { it.isNotBlank() }?.let { ": $it" }.orEmpty()}"
        }
        val fingerprints =
            result.signerCertificates
                .mapTo(linkedSetOf()) { certificate ->
                    MessageDigest.getInstance("SHA-256").digest(certificate.encoded).toHex()
                }
        require(fingerprints.isNotEmpty()) { "APK signature verification returned no signer certificates for $apk" }
        return fingerprints
    }

    private fun ByteArray.toHex(): String = joinToString("") { byte -> "%02x".format(byte) }

    private const val MIN_SUPPORTED_PLATFORM = 24
}

internal fun normalizeSignerFingerprint(raw: String): String {
    val normalized = raw.trim().replace(":", "").lowercase(Locale.ROOT)
    require(normalized.length == 64 && normalized.all { it in '0'..'9' || it in 'a'..'f' }) {
        "signer fingerprint must be a SHA-256 hexadecimal value"
    }
    return normalized
}
