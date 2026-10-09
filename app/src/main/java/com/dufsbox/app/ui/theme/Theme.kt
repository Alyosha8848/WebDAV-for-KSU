package com.dufsbox.app.ui.theme

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat
import com.dufsbox.app.settings.SoftwareSettings

private fun tone(seed: Color, factor: Float, alpha: Float = 1f): Color = Color(
    red = (seed.red * factor).coerceIn(0f, 1f),
    green = (seed.green * factor).coerceIn(0f, 1f),
    blue = (seed.blue * factor).coerceIn(0f, 1f),
    alpha = alpha,
)

private fun lightSchemeFromAccent(seed: Color) = lightColorScheme(
    primary = seed,
    onPrimary = Color.White,
    primaryContainer = tone(seed, 1f).copy(alpha = 0.14f).compositeOver(Color.White),
    onPrimaryContainer = tone(seed, 0.55f),
    secondary = tone(seed, 1.25f).copy(alpha = 0.9f).compositeOver(Color.White),
    onSecondary = Color.White,
    secondaryContainer = tone(seed, 1f).copy(alpha = 0.10f).compositeOver(Color.White),
    onSecondaryContainer = tone(seed, 0.6f),
    tertiary = tone(seed, 0.8f),
    surfaceTint = seed,
    background = tone(seed, 1f).copy(alpha = 0.04f).compositeOver(Color.White),
    surface = tone(seed, 1f).copy(alpha = 0.04f).compositeOver(Color.White),
    surfaceVariant = tone(seed, 1f).copy(alpha = 0.10f).compositeOver(Color.White),
    onSurfaceVariant = tone(seed, 0.65f),
    outline = tone(seed, 1f).copy(alpha = 0.40f).compositeOver(Color.White),
    error = Color(0xFFB3261E),
    onError = Color.White,
    errorContainer = Color(0xFFF9DEDC),
    onErrorContainer = Color(0xFF410E0B),
)

private fun darkSchemeFromAccent(seed: Color) = darkColorScheme(
    primary = seed,
    onPrimary = Color(0xFF10222E),
    primaryContainer = tone(seed, 0.42f),
    onPrimaryContainer = tone(seed, 1.6f).copy(alpha = 0.95f).compositeOver(Color.Black),
    secondary = tone(seed, 0.85f),
    onSecondary = Color(0xFF0E1A22),
    secondaryContainer = tone(seed, 0.35f),
    onSecondaryContainer = tone(seed, 1.5f).compositeOver(Color.Black),
    tertiary = tone(seed, 0.75f),
    surfaceTint = seed,
    background = Color(0xFF101417),
    surface = Color(0xFF14181B),
    surfaceVariant = tone(seed, 0.22f),
    onSurfaceVariant = tone(seed, 1.15f).copy(alpha = 0.9f).compositeOver(Color.Black),
    outline = tone(seed, 0.55f),
    error = Color(0xFFF2B8B5),
    onError = Color(0xFF601410),
    errorContainer = Color(0xFF8C1D18),
    onErrorContainer = Color(0xFFF9DEDC),
)

private tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}

@Composable
fun DufsBoxTheme(
    settings: SoftwareSettings,
    dynamicColorSupported: Boolean,
    content: @Composable () -> Unit,
) {
    val dark = when (settings.themeMode) {
        ThemeMode.SYSTEM -> isSystemInDarkTheme()
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }

    val useDynamic = settings.dynamicColor && dynamicColorSupported && Build.VERSION.SDK_INT >= Build.VERSION_CODES.S

    val colorScheme = when {
        useDynamic -> {
            val ctx = LocalContext.current
            if (dark) dynamicDarkColorScheme(ctx) else dynamicLightColorScheme(ctx)
        }

        dark -> darkSchemeFromAccent(settings.accent.dark)
        else -> lightSchemeFromAccent(settings.accent.light)
    }

    val view = LocalView.current
    if (!view.isInEditMode) {
        SideEffect {
            val activity = view.context.findActivity() ?: return@SideEffect
            WindowCompat.getInsetsController(activity.window, view).apply {
                isAppearanceLightStatusBars = !dark
                isAppearanceLightNavigationBars = !dark
            }
        }
    }

    MaterialTheme(
        colorScheme = colorScheme,
        content = content,
    )
}
