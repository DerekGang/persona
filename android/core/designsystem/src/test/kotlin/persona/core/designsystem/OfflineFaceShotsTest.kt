package persona.core.designsystem

import android.graphics.Bitmap
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "w400dp-h900dp-mdpi")
class OfflineFaceShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun captureBuiltInFaces() {
        val themes = listOf(false, true)
        val sizes = listOf(32, 64)
        val tags = mutableListOf<String>()
        rule.setContent {
            Column {
                for (dark in themes) {
                    PersonaTheme(darkTheme = dark, reduceMotion = true) {
                        for (size in sizes) {
                            Row {
                                for (state in PersonaState.entries) {
                                    val tag = tag(dark, size, state)
                                    Box(Modifier.size(size.dp).testTag(tag)) {
                                        PersonaFace(state, size = size.dp)
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
        rule.waitForIdle()
        for (dark in themes) for (size in sizes) for (state in PersonaState.entries) {
            tags += tag(dark, size, state)
        }
        val out = File("build/offline-face-shots").apply { mkdirs() }
        for (tag in tags) {
            val image = rule.onNodeWithTag(tag).captureToImage().asAndroidBitmap()
            File(out, "$tag.png").outputStream().use {
                assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }
    }

    private fun tag(dark: Boolean, size: Int, state: PersonaState) =
        "${if (dark) "dark" else "light"}-$size-${state.wire}"
}
