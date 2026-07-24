import org.gradle.kotlin.dsl.support.serviceOf
import org.gradle.process.ExecOperations

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
    // ASM — DexStackFrameRewriter recomputes the StackMapTable dex2jar leaves broken on newer
    // extension APKs (GAP-100). compileOnly, same reasoning as the JCEF/bcprov deps below: asm
    // already rides Suwayomi server's RUNTIME classpath transitively (its own BytecodeEditor uses
    // org.objectweb.asm.*), so this only makes the classes visible to the compiler — it adds no new
    // runtime artifact. Version pinned to Suwayomi's libs.versions.toml (`asm = org.ow2.asm:asm`).
    compileOnly("org.ow2.asm:asm:9.9.1")
    // asm-tree: the DexStackFrameRewriter self-instantiation repair (GAP-100 bug (b)) edits
    // instructions and synthesizes a constructor via the ASM tree API. Also compileOnly — asm-tree
    // rides Suwayomi server's runtime classpath transitively via dex2jar (dex-translator depends on it).
    compileOnly("org.ow2.asm:asm-tree:9.9.1")
    // asm-analysis: the DexStackFrameRewriter object-collapse repair (GAP-100 bug (c)) recovers the type
    // dex2jar erased by asking Analyzer/SourceInterpreter which instruction produced a receiver. Also
    // compileOnly — asm-analysis rides Suwayomi server's runtime classpath transitively alongside asm-tree
    // (confirmed present in the built distribution's lib/).
    compileOnly("org.ow2.asm:asm-analysis:9.9.1")
    // BouncyCastleProvider (Main.kt bootstrap, B22 in the P2 bootstrap-hardening audit): the JCE
    // provider at least one real Mihon extension (zh.copymanga) needs for image-URL decryption.
    // compileOnly, same reasoning as the JCEF dep above: bcprov-jdk18on already rides Suwayomi
    // server's RUNTIME classpath transitively (confirmed via `./gradlew dependencies
    // --configuration runtimeClasspath`) — this only makes the already-present class visible to
    // the compiler, it does not add a new runtime artifact. Version pinned to what's actually
    // resolved there.
    compileOnly("org.bouncycastle:bcprov-jdk18on:1.84")
    // androidx.preference stubs + injekt live in AndroidCompat/server; injekt used to resolve CustomContext.
    implementation("com.github.null2264:injekt-koin:ee267b2e27")
    runtimeOnly("ch.qos.logback:logback-classic:1.5.34")

    // JSON for the RPC layer + the extension-repo index parsing (Jackson).
    implementation("com.fasterxml.jackson.core:jackson-databind:2.18.2")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.18.2")

    testImplementation(kotlin("test"))
    // DexStackFrameRewriterTest builds a synthetic broken class with ASM to pin the VerifyError; asm
    // is compileOnly in main (rides Suwayomi's runtime), so the test source needs its own compile dep.
    testImplementation("org.ow2.asm:asm:9.9.1")
    // asm-tree: the object-collapse tests (GAP-100 bug (c)) read the repaired classes back and assert on the
    // `new` instructions themselves — the only way to prove a genuine `new Object` was NOT retargeted.
    testImplementation("org.ow2.asm:asm-tree:9.9.1")
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

// GAP-100 (d): vendor a one-method fix to the dex2jar fork (de.femtopedia.dex2jar:dex-ir:2.4.37)
// until upstream carries it. NewTransformer merges `a = NEW Abc; a.<init>()` into `a = new Abc()`,
// but took the instantiated type from InvokeExpr.getOwner(). When R8/keiyoushi extensions collapse
// an allocation the <init> resolves to java/lang/Object.<init>, so getOwner() erases the concrete
// class to Object — producing a `new Object` that fails a later checkcast (ClassCastException to
// okhttp3.Interceptor; Toonily / Vortex Scans fail to load). vendor/dex2jar/NewTransformer.java reads
// NewExpr.type instead, correct in both the normal and collapsed cases. We recompile ONLY that one
// class against the resolved fork jar and overlay it into the copy installDist ships in lib/, so no
// binary jar is checked into git. Same economy as the compileOnly ASM deps above — no new artifact,
// just a corrected class. See vendor/dex2jar/README.md.

// The resolved fork jar on the runtime classpath: both the compile classpath for the vendored source
// and the archive we overlay the recompiled class into. Resolved lazily (at execution time).
val dex2jarForkJar = configurations.named("runtimeClasspath").map { rc ->
    rc.files.first { it.name == "dex-ir-2.4.37.jar" }
}

// Recompile ONLY NewTransformer against the fork jar (--release 21, via the java{} toolchain).
val compilePatchedDex2jar by tasks.registering(JavaCompile::class) {
    source(layout.projectDirectory.file("vendor/dex2jar/NewTransformer.java"))
    classpath = files(dex2jarForkJar)
    destinationDirectory.set(layout.buildDirectory.dir("dex2jar-patch/classes"))
    options.release.set(21)
}

// Overlay the recompiled com/googlecode/dex2jar/ir/ts/NewTransformer*.class into the fork jar that
// installDist copies into lib/, so the shipped engine carries the fix. `jar uf` replaces the entries
// unconditionally; upToDateWhen(false) forces the overlay to re-run every install, because installDist
// lays down a PRISTINE copy of the fork jar each time — skipping the overlay would ship it unpatched.
val patchInstalledDex2jar by tasks.registering {
    dependsOn(compilePatchedDex2jar)
    val classesDir = compilePatchedDex2jar.flatMap { it.destinationDirectory }
    val installedJar = layout.buildDirectory.file("install/${project.name}/lib/dex-ir-2.4.37.jar")
    // The JDK `jar` tool from the pinned Java-21 toolchain (sits next to javac in the JDK bin).
    val jarTool = javaToolchains.launcherFor {
        languageVersion.set(JavaLanguageVersion.of(21))
    }.map { it.metadata.installationPath.file("bin/jar").asFile.absolutePath }
    // ExecOperations is the Gradle-9 replacement for the removed Project.exec.
    val execOps = serviceOf<ExecOperations>()
    inputs.dir(classesDir)
    outputs.upToDateWhen { false }
    doLast {
        execOps.exec {
            executable = jarTool.get()
            args(
                "uf", installedJar.get().asFile.absolutePath,
                "-C", classesDir.get().asFile.absolutePath,
                "com/googlecode/dex2jar/ir/ts",
            )
        }
    }
}

// installDist must finish with the fork jar patched in place.
tasks.named("installDist") {
    finalizedBy(patchInstalledDex2jar)
}
