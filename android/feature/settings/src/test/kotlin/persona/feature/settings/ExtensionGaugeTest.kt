package persona.feature.settings

import androidx.compose.ui.semantics.ProgressBarRangeInfo
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.ExtensionTile
import persona.core.contracts.api.ExtensionViewAction
import persona.core.contracts.generated.ExtensionGauge
import persona.core.designsystem.PersonaTheme

/** UI version 9's percentage, complete readout, meter range and ordinary tile action. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ExtensionGaugeTest {
    @get:Rule val rule = createComposeRule()

    @Test fun percentageClampsAndRoundsLikeTheSharedContract() {
        assertEquals(0, gaugePercent(Double.NaN))
        assertEquals(0, gaugePercent(Double.NEGATIVE_INFINITY))
        assertEquals(0, gaugePercent(Double.POSITIVE_INFINITY))
        assertEquals(0, gaugePercent(-0.2))
        assertEquals(42, gaugePercent(0.424))
        assertEquals(43, gaugePercent(0.425))
        assertEquals(100, gaugePercent(1.2))
    }

    @Test fun readoutUsesValueAndUnitOnlyWhenAValueExists() {
        assertEquals(
            "Storage: 42 GB, 42%",
            gaugeReadout(ExtensionTile(title = "Storage", value = "42", gauge = ExtensionGauge(0.424, "GB"))),
        )
        assertEquals(
            "Storage: 42%",
            gaugeReadout(ExtensionTile(title = "Storage", gauge = ExtensionGauge(0.424, "GB"))),
        )
        assertEquals(
            "Storage: 42, 42%",
            gaugeReadout(ExtensionTile(title = "Storage", value = "42", gauge = ExtensionGauge(0.424))),
        )
        assertEquals(
            "Storage: 42",
            gaugeReadout(ExtensionTile(title = "Storage", value = "42")),
        )
    }

    @Test fun actionableGaugeTileKeepsMeterAndButtonSemantics() {
        val action = ExtensionViewAction("open-storage", values = mapOf("source" to "gauge"))
        var pressed: ExtensionViewAction? = null
        val tile = ExtensionTile(
            title = "Storage",
            value = "42",
            gauge = ExtensionGauge(0.424, "GB"),
            action = action,
        )
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                ExtensionTileView(tile, ExtensionViewState(), ExtensionViewHandle(
                    press = { _, sent -> pressed = sent },
                    edit = { _, _ -> },
                    choose = {},
                    open = {},
                ))
            }
        }
        val node = rule.onNodeWithContentDescription("Storage: 42 GB, 42%")
        node.assertIsDisplayed()
        val semantics = node.fetchSemanticsNode().config
        assertEquals(ProgressBarRangeInfo(42f, 0f..100f), semantics[SemanticsProperties.ProgressBarRangeInfo])
        node.performClick()
        assertEquals(action, pressed)
    }
}
