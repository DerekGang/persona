package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.ExtensionViewItem
import persona.core.contracts.generated.ExtensionMapCoordinate
import persona.core.contracts.generated.ExtensionMapLink
import persona.core.contracts.generated.ExtensionMapMarker
import persona.core.contracts.generated.ExtensionMapRoute
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "w400dp-h1000dp-mdpi")
class ExtensionMapShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun fitsRoutesAcrossTheDateLineAndAcceptsOnlyHttpsLinks() {
        val west = ExtensionMapCoordinate(10.0, 179.8)
        val east = ExtensionMapCoordinate(11.0, -179.7)
        val frame = checkNotNull(mapFrame(listOf(west, east), null))
        assertTrue(frame.spanLon < 1.0)
        assertNull(mapFrame(listOf(ExtensionMapCoordinate(110.0, 45.0)), null))
        assertEquals("https://example.com/route", mapExternalLink("https://example.com/route"))
        assertNull(mapExternalLink("http://example.com/route"))
        assertNull(mapExternalLink("https:///route"))
    }

    @Test fun routesRenderInEveryStyleAndOpenAnInteractiveFullScreen() {
        val first = ExtensionMapCoordinate(24.45, 118.06)
        val second = ExtensionMapCoordinate(24.46, 118.08)
        val item = ExtensionViewItem(type = "map", title = "Harbour route", alt = "Harbour and park map", showList = true,
            routes = listOf(ExtensionMapRoute("a", "By the harbour", distanceMeters = 1400, durationSeconds = 1200, points = listOf(first, second)),
                ExtensionMapRoute("b", "Through the park", summary = "Scenic route", points = listOf(first, ExtensionMapCoordinate(24.44, 118.079), second))),
            markers = listOf(ExtensionMapMarker("start", first.lat, first.lon, label = "Harbour"),
                ExtensionMapMarker("end", second.lat, second.lon, label = "Park")),
            links = listOf(ExtensionMapLink("Open route", "https://example.com/route")))
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var shown by mutableStateOf(item)
        val opened = mutableListOf<String>()
        val handle = ExtensionViewHandle({ _, _ -> }, { _, _ -> }, {}, { opened += it })
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.width(360.dp).background(PersonaTheme.palette.background)) {
                    ExtensionMap(shown, ExtensionViewState(), handle)
                }
            }
        }
        val output = File(System.getProperty("persona.shots") ?: "build/extension-map-shots").also { it.mkdirs() }
        for (appearance in AppStyle.entries) for (night in listOf(false, true)) {
            rule.runOnIdle { style = appearance; dark = night }
            rule.waitForIdle()
            val image = rule.onNodeWithTag("extension-map").captureToImage().asAndroidBitmap()
            val name = "${appearance.name.lowercase()}-${if (night) "dark" else "light"}.png"
            File(output, name).outputStream().use { image.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        rule.onNodeWithTag("extension-map-canvas").performClick()
        rule.onNodeWithTag("extension-map-full-screen").assertExists()
        rule.onNodeWithText("Zoom in").performClick()
        rule.onNodeWithText("Reset map").performClick()
        val scenic = rule.onNode(hasTestTag("extension-map-route-b") and hasAnyAncestor(hasTestTag("extension-map-full-screen")))
        scenic.performScrollTo().performClick().assertIsSelected()
        rule.runOnIdle {
            shown = item.copy(showList = false, routes = item.routes.orEmpty() +
                ExtensionMapRoute("c", "Along the bank", points = listOf(first, second)))
        }
        scenic.assertIsSelected()
        assertTrue(opened.isEmpty())
    }
}
