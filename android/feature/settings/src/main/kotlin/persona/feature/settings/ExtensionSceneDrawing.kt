package persona.feature.settings

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipPath
import persona.core.contracts.generated.ExtensionSceneMaterial
import kotlin.math.ceil
import kotlin.math.sqrt

internal data class SceneLamp(val position: SceneVector, val cool: Boolean)

private val sceneKeyLight = SceneVector(-0.36, -0.48, 0.8)

internal fun sceneFaceColor(face: SceneFace, lamps: List<SceneLamp>, dark: Boolean): Color {
    val daylight = face.normal.dot(sceneKeyLight).coerceAtLeast(0.0)
    val ambient = if (dark) 0.54 else 0.76
    val gloss = when (face.material) {
        ExtensionSceneMaterial.METAL -> 0.20
        ExtensionSceneMaterial.SCREEN -> 0.16
        else -> 0.0
    }
    val natural = ambient + daylight * (0.28 + gloss)
    var warmth = 0.0
    var coolness = 0.0
    for (lamp in lamps) {
        val toward = lamp.position - face.center
        val distanceSquared = toward.dot(toward).coerceAtLeast(0.04)
        val angle = (face.normal.dot(toward) / sqrt(distanceSquared)).coerceIn(0.0, 1.0)
        val brightness = angle * 0.72 / (1 + distanceSquared)
        if (lamp.cool) coolness += brightness else warmth += brightness
    }
    fun channel(shift: Int, light: Double) = ((face.color shr shift and 255) / 255.0 * light).coerceIn(0.0, 1.0).toFloat()
    return Color(
        red = channel(16, natural + warmth + coolness * 0.84),
        green = channel(8, natural + warmth * 0.72 + coolness * 0.92),
        blue = channel(0, natural + warmth * 0.34 + coolness),
        alpha = if (face.material == ExtensionSceneMaterial.GLASS) 0.46f else 1f,
    )
}

private fun ScenePoint.offset() = Offset(x.toFloat(), y.toFloat())

internal fun DrawScope.drawExtensionScene(
    mesh: SceneMesh,
    projection: SceneProjection,
    lamps: List<SceneLamp>,
    dark: Boolean,
    edgeWidth: Float,
) {
    val points = mesh.vertices.map { projection.position(it).offset() }
    for (face in mesh.visibleFaces(projection.view)) {
        val start = points[face.vertices.first()]
        val path = Path().apply {
            moveTo(start.x, start.y)
            for (index in face.vertices.drop(1)) {
                val point = points[index]
                lineTo(point.x, point.y)
            }
            close()
        }
        val color = sceneFaceColor(face, lamps, dark)
        drawPath(path, color)
        if (face.material == ExtensionSceneMaterial.GLASS) {
            drawPath(path, color.copy(alpha = 0.75f), style = Stroke(edgeWidth))
        } else if (face.normal.z > 0.5) {
            drawSurfacePattern(mesh, face, projection, path, color, edgeWidth)
        }
    }
}

private fun DrawScope.drawSurfacePattern(
    mesh: SceneMesh,
    face: SceneFace,
    projection: SceneProjection,
    path: Path,
    color: Color,
    edgeWidth: Float,
) {
    val step = when (face.material) {
        ExtensionSceneMaterial.TILE -> 0.42
        ExtensionSceneMaterial.WOOD -> 0.16
        else -> return
    }
    val corners = face.vertices.map(mesh.vertices::get)
    val minX = corners.minOf { it.x }
    val maxX = corners.maxOf { it.x }
    val minY = corners.minOf { it.y }
    val maxY = corners.maxOf { it.y }
    val z = face.center.z
    val seam = Color(color.red * 0.63f, color.green * 0.63f, color.blue * 0.63f, 0.34f)
    clipPath(path) {
        if (face.material == ExtensionSceneMaterial.TILE) {
            for (x in repeatedSeams(minX, maxX, step)) {
                drawLine(seam, projection.position(SceneVector(x, minY, z)).offset(),
                    projection.position(SceneVector(x, maxY, z)).offset(), edgeWidth)
            }
        }
        for (y in repeatedSeams(minY, maxY, step)) {
            drawLine(seam, projection.position(SceneVector(minX, y, z)).offset(),
                projection.position(SceneVector(maxX, y, z)).offset(), edgeWidth)
        }
    }
}

internal fun repeatedSeams(minimum: Double, maximum: Double, spacing: Double): Sequence<Double> {
    val span = maximum - minimum
    if (!span.isFinite() || span <= 0.0) return emptySequence()
    val step = spacing.coerceAtLeast(span / 64)
    // Anchor to this face's own minimum rather than world zero: separate floor prisms with one material then keep a
    // shape-local pattern, and a shape narrower than the fixed step still receives seams (review P2).
    val first = minimum + step
    return generateSequence(first) { it + step }.take(64).takeWhile { it <= maximum + 1e-9 }
}
