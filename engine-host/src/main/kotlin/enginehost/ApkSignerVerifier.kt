package enginehost

import com.android.apksig.ApkVerifier
import java.nio.file.Path
import java.security.MessageDigest
import java.util.Locale

internal fun interface ApkSignatureVerifier {
    fun verify(apk: Path): Set<String>

    fun verifyIdentity(apk: Path): VerifiedApkSignature = VerifiedApkSignature(verify(apk), emptyList())
}

/** Current signer identity plus an oldest-to-newest cryptographically verified proof-of-rotation lineage. */
internal data class VerifiedApkSignature(
    val currentSignerFingerprints: Set<String>,
    val signingCertificateLineage: List<String>,
) {
    /**
     * Preserve installed continuity without letting lineage grant repository trust. Exact current
     * signer sets remain compatible; a changed signer is accepted only for a single-signer APK when
     * the installed current signer is an ancestor of the candidate current signer in the verified
     * lineage. The independent repository pin still authorizes the candidate current signer.
     */
    fun continuesFrom(installed: VerifiedApkSignature): Boolean {
        if (currentSignerFingerprints == installed.currentSignerFingerprints) return true
        if (currentSignerFingerprints.size != 1 || installed.currentSignerFingerprints.size != 1) return false
        val current = currentSignerFingerprints.single()
        val ancestor = installed.currentSignerFingerprints.single()
        val currentIndex = signingCertificateLineage.lastIndexOf(current)
        val ancestorIndex = signingCertificateLineage.indexOf(ancestor)
        return currentIndex == signingCertificateLineage.lastIndex && ancestorIndex in 0 until currentIndex
    }
}

/** Cryptographically verify an APK and return its current signer plus verified signing lineage. */
internal object ApkSignerVerifier : ApkSignatureVerifier {
    override fun verify(apk: Path): Set<String> = verifyIdentity(apk).currentSignerFingerprints

    override fun verifyIdentity(apk: Path): VerifiedApkSignature {
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
                .mapTo(linkedSetOf()) { certificate -> certificate.fingerprint() }
        require(fingerprints.isNotEmpty()) { "APK signature verification returned no signer certificates for $apk" }
        val lineage =
            result.signingCertificateLineage
                ?.certificatesInLineage
                ?.map { certificate -> certificate.fingerprint() }
                .orEmpty()
        return VerifiedApkSignature(fingerprints, lineage)
    }

    private fun java.security.cert.Certificate.fingerprint(): String =
        MessageDigest.getInstance("SHA-256").digest(encoded).toHex()

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
