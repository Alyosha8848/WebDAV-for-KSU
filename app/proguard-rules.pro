# DufsBox / app — ProGuard rules.

# libsu
-keep class com.topjohnwu.superuser.** { *; }
-keepclassmembers class com.topjohnwu.superuser.** { *; }
-dontwarn com.topjohnwu.superuser.**

# org.json is part of the platform; nothing to keep, but silence R8 notes.
-dontwarn org.json.**

# Kotlin metadata / coroutines
-dontwarn kotlinx.coroutines.**
-keepclassmembers class kotlinx.coroutines.** { volatile <fields>; }

# Keep our data classes (defensive JSON parsing uses reflection-free code, but this
# keeps stack traces readable when a release build is debugged).
-keep class com.dufsbox.app.data.model.** { *; }

# Compose
-dontwarn androidx.compose.**
