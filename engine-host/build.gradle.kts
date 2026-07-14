plugins {
    kotlin("jvm") version "2.4.0"
    application
}

group = "digital.redark.tsundoku"
version = "0.1.0-p1"

dependencies {
    implementation(kotlin("stdlib"))
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.10.2")

    // Suwayomi-Server's OWN code (composite-build substitution → its :server + AndroidCompat
    // projects). Brings the eu.kanade.tachiyomi source-api + network stack, the extension
    // loaders (PackageTools/dex2jar/ChildFirstURLClassLoader), the android.* shims, KCEF WebView,
    // CEFManager, the CloudflareInterceptor, and ServerConfig (flareSolverr/socks/kcef flags).
    implementation("suwayomi:server")
    implementation("suwayomi:AndroidCompat")
    implementation("suwayomi:server-config") // ServerConfig / ConfigTypeRegistration
    implementation("suwayomi:Config") // xyz.nulldev.ts.config.* (GlobalConfigManager, ApplicationRootDir)

    // server/AndroidCompat expose these as `implementation` (not api), so the host must
    // declare the ones it references directly. Versions pinned to Suwayomi's libs.versions.toml.
    implementation("io.insert-koin:koin-core:4.2.2")
    implementation("com.squareup.okhttp3:okhttp:5.4.0")
    implementation("io.github.oshai:kotlin-logging-jvm:8.0.4")
    implementation("org.slf4j:slf4j-api:2.0.18")
    implementation("com.typesafe:config:1.4.9") // Config type referenced by ServerConfig.register
    // JCEF types (CefCookieManager) referenced by the KCEF cookie-seed handler. compileOnly:
    // the actual classes ride Suwayomi server's runtime classpath. Pinned to Suwayomi's libs.
    compileOnly("org.jetbrains.intellij.deps.jcef:jcef:144.0.15-g72717cf-chromium-144.0.7559.172-api-1.21-262-b37")
    // androidx.preference stubs + injekt live in AndroidCompat/server; injekt used to resolve CustomContext.
    implementation("com.github.null2264:injekt-koin:ee267b2e27")
    runtimeOnly("ch.qos.logback:logback-classic:1.5.34")

    // JSON for the RPC layer + the extension-repo index parsing (Jackson).
    implementation("com.fasterxml.jackson.core:jackson-databind:2.18.2")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.18.2")

    testImplementation(kotlin("test"))
}

application {
    mainClass.set("enginehost.MainKt")
}

// Pin the Java toolchain to 21 (reproducible; JDK 17 CANNOT build AndroidCompat's Java sources,
// which target --release 21). The build no longer silently depends on the machine JDK.
kotlin {
    jvmToolchain(21)
}

java {
    toolchain {
        languageVersion.set(JavaLanguageVersion.of(21))
    }
}

tasks.withType<JavaExec> {
    // The Android main-loop + KCEF like large stacks; keep parity with Suwayomi defaults.
    jvmArgs("-Xmx1g")
}

tasks.withType<Test> {
    useJUnitPlatform()
}
