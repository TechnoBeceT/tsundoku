pluginManagement {
    repositories {
        gradlePluginPortal()
        mavenCentral()
    }
}

// Auto-provision the pinned Java-21 toolchain (build.gradle.kts) so the build never silently
// depends on the machine JDK. Requires network on first use; cached thereafter.
plugins {
    id("org.gradle.toolchains.foojay-resolver-convention") version "1.0.0"
}

rootProject.name = "tsundoku-engine-host"

// Reuse Suwayomi-Server's OWN JVM-native code (AndroidCompat + the eu.kanade.tachiyomi
// source-api + extension loaders) via a Gradle composite build. Dependency substitution
// wires `suwayomi:server` (declared in build.gradle.kts) to the included build's :server
// project, so the host links against Suwayomi's real classes with NO Suwayomi server/DB.
// NOTE: absolute path for local dev; the Docker build stage (Task 8) vendors the Suwayomi
// source and overrides this via the `suwayomiSrc` gradle property.
includeBuild(providers.gradleProperty("suwayomiSrc").getOrElse("/home/technobecet/Projects/Examples/Suwayomi-Server"))

dependencyResolutionManagement {
    repositories {
        mavenCentral()
        google()
        maven("https://github.com/Suwayomi/Suwayomi-Server/raw/android-jar/")
        maven("https://jitpack.io")
        maven("https://jogamp.org/deployment/maven")
        maven("https://packages.jetbrains.team/maven/p/ij/intellij-dependencies")
    }
}
