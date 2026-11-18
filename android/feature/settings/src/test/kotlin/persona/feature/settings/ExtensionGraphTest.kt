package persona.feature.settings

import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertHasNoClickAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.StateRestorationTester
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.layout.width
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.ExtensionViewItem
import persona.core.contracts.generated.ExtensionAction
import persona.core.contracts.generated.ExtensionBadge
import persona.core.contracts.generated.ExtensionGraphEdge
import persona.core.contracts.generated.ExtensionGraphNode
import persona.core.contracts.generated.ExtensionPush
import persona.core.designsystem.PersonaTheme
import kotlin.math.abs

/** Graph transform bounds, action dispatch, folding, full-screen continuity and compact-preview boundaries. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "w400dp-h1000dp-mdpi")
class ExtensionGraphTest {
    @get:Rule val rule = createComposeRule()

    private val nodes = listOf(
        ExtensionGraphNode(
            id = "root", title = "Plan", subtitle = "3 steps", progress = 0.25,
            badge = ExtensionBadge(text = "Active"),
            action = ExtensionAction(
                id = "open-plan", confirm = "Open the plan?", submit = true,
                push = ExtensionPush(state = "step=1", title = "Plan", ui = "plan"),
                values = mapOf("node" to "root"),
            ),
        ),
        ExtensionGraphNode(id = "design", title = "Design", subtitle = "Ready", group = "Work"),
        ExtensionGraphNode(id = "build", title = "Build", subtitle = "Waiting", group = "Work"),
    )
    private val item = ExtensionViewItem(
        type = "graph", title = "Delivery graph", key = "delivery", nodes = nodes,
        edges = listOf(ExtensionGraphEdge("root", "design"), ExtensionGraphEdge("root", "build")),
        selected = "design",
    )

    @Test fun openingAndExplicitFitUseTheirOwnZoomBounds() {
        val viewport = Size(400f, 300f)
        val world = Size(800f, 600f)
        val opening = fitExtensionGraph(viewport, world, 16f, opening = true)
        assertEquals(0.5f, opening.scale)
        val fit = fitExtensionGraph(viewport, world, 16f, opening = false)
        assertEquals(268f / 600f, fit.scale, 1e-6f)
        assertEquals(1f, fitExtensionGraph(viewport, Size(100f, 50f), 16f, opening = true).scale)

        val focused = fitExtensionGraph(
            viewport, world, 16f, opening = true,
            focus = Rect(200f, 100f, 400f, 200f),
        )
        val centre = Offset(300f, 150f) * focused.scale + focused.pan
        assertEquals(200f, centre.x, 1e-5f)
        assertEquals(150f, centre.y, 1e-5f)
    }

    @Test fun zoomClampsAndPanKeepsEveryLargeGraphEdgeReachable() {
        var transform = ExtensionGraphTransform(1f, Offset.Zero)
        repeat(20) {
            transform = zoomExtensionGraph(transform, 2f, Size(400f, 300f), Size(800f, 600f), 16f)
        }
        assertEquals(2f, transform.scale)
        repeat(40) {
            transform = zoomExtensionGraph(transform, 0.5f, Size(400f, 300f), Size(800f, 600f), 16f)
        }
        assertEquals(0.25f, transform.scale)

        val clamped = clampExtensionGraphPan(
            Offset(1000f, -1000f), 2f, Size(400f, 300f), Size(800f, 600f), 16f,
        )
        assertEquals(16f, clamped.x)
        assertEquals(300f - 16f - 1200f, clamped.y)
        assertEquals(
            Offset(100f, 75f),
            clampExtensionGraphPan(Offset.Zero, 0.25f, Size(400f, 300f), Size(800f, 600f), 16f),
        )
    }

    @Test fun nodeActionsAndFoldControlsStaySeparate() {
        var pressed: Pair<ExtensionViewItem, persona.core.contracts.api.ExtensionViewAction>? = null
        mount(item) { view, action -> pressed = view to action }
        rule.onNodeWithContentDescription("Plan, Active, 3 steps, 25%").assertIsDisplayed()
        rule.onNodeWithContentDescription("Design, Ready, under Plan").assertExists()
            .assertHasNoClickAction()
        rule.onNodeWithTag("graph-node-root").performClick()
        val (view, action) = pressed!!
        assertEquals("row", view.type)
        assertEquals("delivery/root", view.key)
        assertEquals("open-plan", action.id)
        assertEquals("Open the plan?", action.confirm)
        assertTrue(action.submit)
        assertEquals("step=1", action.push?.state)
        assertEquals(mapOf("node" to "root"), action.values)

        pressed = null
        rule.onNodeWithTag("graph-fold-root").performClick()
        assertEquals(null, pressed)
        rule.onNodeWithTag("graph-node-design").assertDoesNotExist()
        rule.onNodeWithTag("graph-node-build").assertDoesNotExist()
        rule.onNodeWithContentDescription("Show 2 hidden").performClick()
        rule.onNodeWithTag("graph-node-design").assertExists()
        rule.onNodeWithTag("graph-node-build").assertExists()
    }

    @Test fun zoomAndFoldsContinueIntoFullScreenAndBack() {
        mount(item)
        rule.onNodeWithTag("graph-zoom-in").performClick()
        val before = rule.onNodeWithTag("graph-node-root").fetchSemanticsNode().boundsInRoot.width
        rule.onNodeWithTag("graph-open-full").performClick()
        val inDialog = hasTestTag("graph-node-root") and hasAnyAncestor(isDialog())
        rule.onNode(inDialog).assertIsDisplayed()
        val after = rule.onNode(inDialog).fetchSemanticsNode().boundsInRoot.width
        assertTrue("zoom survives full screen: $before vs $after", abs(before - after) < 1f)
        rule.onNode(hasTestTag("graph-fold-root") and hasAnyAncestor(isDialog())).performClick()
        rule.onNodeWithContentDescription("Back").performClick()
        rule.onNodeWithTag("graph-node-design").assertDoesNotExist()
        rule.onNodeWithContentDescription("Show 2 hidden").assertExists()
    }

    @Test fun transformAndFoldsSurviveSavedStateRestoration() {
        val restoration = StateRestorationTester(rule)
        restoration.setContent {
            PersonaTheme(reduceMotion = true) {
                ExtensionGraph(item, ExtensionViewState(), ExtensionViewHandle(
                    press = { _, _ -> }, edit = { _, _ -> }, choose = {}, open = {},
                ), Modifier.width(400.dp))
            }
        }
        rule.onNodeWithTag("graph-zoom-in").performClick()
        val zoomedWidth = rule.onNodeWithTag("graph-node-root").fetchSemanticsNode().boundsInRoot.width
        rule.onNodeWithTag("graph-fold-root").performClick()
        restoration.emulateSavedInstanceStateRestore()
        rule.onNodeWithTag("graph-node-design").assertDoesNotExist()
        rule.onNodeWithContentDescription("Show 2 hidden").assertExists()
        assertEquals(
            zoomedWidth,
            rule.onNodeWithTag("graph-node-root").fetchSemanticsNode().boundsInRoot.width,
            1f,
        )
    }

    @Test fun compactPreviewHasNoInnerActionsAndOnlyOpensFullScreen() {
        var pressed = false
        mount(item, compact = true) { _, _ -> pressed = true }
        rule.onNodeWithTag("graph-node-root").assertHasNoClickAction()
        rule.onAllNodesWithTag("graph-zoom-in").assertCountEquals(0)
        rule.onAllNodesWithTag("graph-fold-root").assertCountEquals(0)
        rule.onNodeWithContentDescription("Open graph full screen").assertHasClickAction().performClick()
        rule.onNodeWithTag("graph-full-screen").assertIsDisplayed()
        rule.onNode(hasTestTag("graph-zoom-in") and hasAnyAncestor(isDialog())).assertIsEnabled()
        assertEquals(false, pressed)
    }

    @Test fun untouchedRedrawRefitsButTouchedRedrawKeepsItsPosition() {
        val current = mutableStateOf(item)
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                ExtensionGraph(current.value, ExtensionViewState(), ExtensionViewHandle(
                    press = { _, _ -> }, edit = { _, _ -> }, choose = {}, open = {},
                ), Modifier.width(400.dp))
            }
        }
        val before = rule.onNodeWithTag("graph-node-design").fetchSemanticsNode().boundsInRoot.center
        current.value = item.copy(selected = "build")
        rule.waitForIdle()
        val refitted = rule.onNodeWithTag("graph-node-design").fetchSemanticsNode().boundsInRoot.center
        assertTrue("an untouched graph takes its new opening view", abs(before.x - refitted.x) + abs(before.y - refitted.y) > 1f)

        rule.onNodeWithTag("graph-zoom-in").performClick()
        val moved = rule.onNodeWithTag("graph-node-root").fetchSemanticsNode().boundsInRoot
        current.value = item.copy(selected = "root")
        rule.waitForIdle()
        assertEquals(moved, rule.onNodeWithTag("graph-node-root").fetchSemanticsNode().boundsInRoot)
    }

    @Test fun changingGraphKeyStartsWithANewOpeningTransform() {
        val current = mutableStateOf(item)
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                ExtensionGraph(current.value, ExtensionViewState(), ExtensionViewHandle(
                    press = { _, _ -> }, edit = { _, _ -> }, choose = {}, open = {},
                ), Modifier.width(400.dp))
            }
        }
        val initial = rule.onNodeWithTag("graph-node-root").fetchSemanticsNode().boundsInRoot.width
        rule.onNodeWithTag("graph-zoom-in").performClick()
        assertTrue(rule.onNodeWithTag("graph-node-root").fetchSemanticsNode().boundsInRoot.width > initial)
        current.value = item.copy(key = "another-graph")
        rule.waitForIdle()
        assertEquals(initial, rule.onNodeWithTag("graph-node-root").fetchSemanticsNode().boundsInRoot.width, 1f)
    }

    @Test fun zoomButtonsStopAtTwentyFiveAndTwoHundredPercent() {
        mount(item)
        repeat(8) { rule.onNodeWithTag("graph-zoom-in").performClick() }
        rule.onNodeWithTag("graph-zoom-in").assertIsNotEnabled()
        repeat(10) { rule.onNodeWithTag("graph-zoom-out").performClick() }
        rule.onNodeWithTag("graph-zoom-out").assertIsNotEnabled()
        rule.onNodeWithTag("graph-fit").performClick()
        rule.onNodeWithTag("graph-zoom-in").assertIsEnabled()
        rule.onNodeWithTag("graph-zoom-out").assertIsEnabled()
    }

    private fun mount(
        graph: ExtensionViewItem,
        compact: Boolean = false,
        press: (ExtensionViewItem, persona.core.contracts.api.ExtensionViewAction) -> Unit = { _, _ -> },
    ) {
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                ExtensionGraph(
                    graph, ExtensionViewState(), ExtensionViewHandle(
                        press = press, edit = { _, _ -> }, choose = {}, open = {},
                    ),
                    Modifier.width(400.dp).testTag("fixture"), compact = compact,
                )
            }
        }
    }
}
