package persona.feature.agents

import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalHomeChrome
import persona.core.designsystem.PersonaTheme
import persona.core.domain.sort.AgentGrouping
import java.io.File

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1200dp-xhdpi")
class SeenRowsShotsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test fun fiveSeenRowsStayVisibleAndClearTogetherInEveryStyle() {
        val sessions = listOf("Check the release", "Review the notes", "Verify the build", "Update the docs", "Confirm the tests")
            .mapIndexed { index, title ->
                Session("mac", "job-$index", title = title, cwd = "/home/example/project", state = "unread",
                    updatedAt = "2026-01-01T00:00:0${5 - index}Z")
            }
        val model = agentsModel(sessions, listOf(Machine("mac", "Example computer", online = true)))
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                CompositionLocalProvider(LocalHomeChrome provides agentsHomeChrome) {
                    AgentsScreen(model, NoNavigation, AgentsMetadata())
                }
            }
        }
        rule.waitUntil(10_000) { model.page.value.loaded }
        rule.runOnIdle {
            model.grouping(AgentGrouping.State)
            model.range(AgentRange.All)
        }
        rule.waitUntil(10_000) { model.page.value.list.groups.any { it.id == "state/unread" && it.rows.size == 5 } }
        for (session in sessions) {
            rule.runOnIdle {
                val row = model.page.value.list.groups.single { it.id == "state/unread" }.rows.single { it.id == session.key.path }
                model.markRead(listOf(row))
            }
            rule.waitUntil(10_000) {
                model.page.value.list.groups.singleOrNull { it.id == "state/unread" }?.heldSeen?.contains(session.key.path) == true
            }
        }
        val output = File(System.getProperty("persona.shots") ?: "build/seen-rows-shots").also { it.mkdirs() }
        for (appearance in AppStyle.entries) for (night in listOf(false, true)) {
            rule.runOnIdle {
                style = appearance
                dark = night
            }
            rule.onNodeWithText("Clear Seen (5)").assertIsDisplayed()
            sessions.forEach { rule.onNodeWithText(checkNotNull(it.title)).assertIsDisplayed() }
            val variant = if (night) "dark" else "light"
            val bitmap = rule.onRoot().captureToImage().asAndroidBitmap()
            File(output, "seen-rows-${appearance.name.lowercase()}-$variant.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        rule.onNodeWithText("Clear Seen (5)").performClick()
        rule.waitUntil(10_000) { model.page.value.list.groups.none { it.id == "state/unread" } }
        rule.onNodeWithText("Clear Seen (5)").assertDoesNotExist()
    }
}
