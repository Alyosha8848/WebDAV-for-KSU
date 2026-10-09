import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    // AGP 9 provides Kotlin itself; `org.jetbrains.kotlin.android` must NOT be
    // applied (AGP 9 fails the build if it is). Only the Compose compiler plugin
    // is needed, and its version must match the Kotlin version AGP bundles (2.2.10).
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "com.dufsbox.app"

    // Two different things, deliberately kept apart:
    //   compileSdk = which SDK the code is compiled against. Current AndroidX /
    //                Compose artifacts (core 1.19.1, compose 1.12.1) declare in
    //                their AAR metadata that they require compiling against
    //                API 37 or newer, so 36 cannot be used with them.
    //   targetSdk  = the Android runtime behaviour the app opts into. This stays
    //                36 (Android 16), which is the platform DufsBox targets;
    //                raising compileSdk does not change runtime behaviour.
    compileSdk = 37

    defaultConfig {
        applicationId = "com.dufsbox.app"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "1.0.0"

        // The KernelSU module ships arm64 binaries only.
        ndk {
            abiFilters += listOf("arm64-v8a")
        }
    }

    // Java 17 (AGP 9 requires a modern toolchain). No `kotlinOptions` — it is gone in AGP 9.
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        // buildConfig stays off: it is not needed anywhere in this module.
    }

    buildTypes {
        debug {
            isMinifyEnabled = false
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }
    }

    packaging {
        resources {
            excludes += setOf(
                "/META-INF/{AL2.0,LGPL2.1}",
                "/META-INF/DEPENDENCIES",
                "/META-INF/LICENSE*",
                "/META-INF/NOTICE*",
            )
        }
    }

    lint {
        abortOnError = false
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2026.09.00"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")

    implementation("androidx.core:core-ktx:1.19.1")
    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.11.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.11.0")

    // Persistent root shell (no :service artifact on purpose — see docs/API.md §6).
    implementation("com.github.topjohnwu.libsu:core:5.2.2")

    debugImplementation("androidx.compose.ui:ui-tooling")
}
