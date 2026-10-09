// DufsBox — root build script.
//
// Versions are declared inline (no version catalog on purpose).
//
// AGP 9 has built-in Kotlin support and *rejects* the `org.jetbrains.kotlin.android`
// plugin ("no longer required for Kotlin support since AGP 9.0"). AGP 9.4.1 brings
// kotlin-gradle-plugin 2.2.10 on its own classpath, which is also the version the
// Compose compiler plugin must match exactly.
plugins {
    id("com.android.application") version "9.4.1" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.2.10" apply false
}
