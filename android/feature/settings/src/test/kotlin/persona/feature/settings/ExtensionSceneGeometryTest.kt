package persona.feature.settings

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.generated.ExtensionSceneBox
import persona.core.contracts.generated.ExtensionSceneCamera
import persona.core.contracts.generated.ExtensionSceneCylinder
import persona.core.contracts.generated.ExtensionScenePrism
import persona.core.contracts.generated.ExtensionSceneShape

class ExtensionSceneGeometryTest {
    private val box = ExtensionSceneBox(
        center = listOf(0.0, 0.0), size = listOf(4.0, 2.0),
        bottom = 0.0, top = 1.0, color = "#658197",
    )

    @Test fun cameraUsesWireDefaultsAndLimitsOrbitAndZoom() {
        assertEquals(SceneCamera(225.0, 35.0, 1.0), SceneCamera.from(null))
        val camera = SceneCamera.from(ExtensionSceneCamera(yaw = -90.0, pitch = 25.0, zoom = 1.5))
        assertEquals(SceneCamera(270.0, 25.0, 1.5), camera)
        assertEquals(SceneCamera(10.0, 10.0, 4.0), camera.orbit(100.0, -45.0).zoomBy(5.0))
        assertEquals(0.5, camera.zoomBy(0.1).zoom, 1e-8)
    }

    @Test fun knownShapesUseMetresAndBoxYawTurnsCounterClockwise() {
        val mesh = requireNotNull(SceneMesh.from(listOf(box.copy(yaw = 90.0))))
        assertEquals(1.0, mesh.vertices.maxOf { it.x }, 1e-8)
        assertEquals(2.0, mesh.vertices.maxOf { it.y }, 1e-8)
        assertEquals(1.0, mesh.vertices.maxOf { it.z }, 1e-8)
        assertTrue(mesh.faces.all { it.color == 0x658197 })
        val rounded = requireNotNull(SceneMesh.from(listOf(box.copy(round = 0.3))))
        assertTrue(rounded.faces.size > mesh.faces.size)
        val cylinder = ExtensionSceneCylinder(center = listOf(0.0, 0.0), radius = 0.3, bottom = 0.0, top = 0.4)
        assertEquals(0.3, requireNotNull(SceneMesh.from(listOf(cylinder))).vertices.maxOf { it.x }, 1e-8)
    }

    @Test fun concavePrismKeepsItsPerimeterAndOutwardNormalsInEitherOrder() {
        val outline = listOf(listOf(0.0, 0.0), listOf(2.0, 0.0), listOf(2.0, 1.0),
            listOf(1.0, 1.0), listOf(1.0, 2.0), listOf(0.0, 2.0))
        val prism = ExtensionScenePrism(points = outline, bottom = 0.0, top = 0.08)
        val forward = requireNotNull(SceneMesh.from(listOf(prism)))
        val reversed = requireNotNull(SceneMesh.from(listOf(prism.copy(points = outline.reversed()))))
        assertEquals(forward.vertices, reversed.vertices)
        assertEquals(6, forward.faces.single { it.normal.z == 1.0 }.vertices.size)
        assertEquals(SceneVector(0.0, -1.0, 0.0), forward.faces.first { it.normal.y == -1.0 }.normal)
    }

    @Test fun topDownFitUsesJustShapeBoundsAndZoomKeepsTheCenter() {
        val mesh = requireNotNull(SceneMesh.from(listOf(box)))
        val camera = SceneCamera(0.0, 90.0, 1.0)
        val fit = SceneProjection.fit(camera, mesh.bounds, 300f, 300f, 20f)
        assertEquals(ScenePoint(150.0, 150.0), fit.position(mesh.bounds.center))
        assertTrue(mesh.vertices.all {
            val point = fit.position(it)
            point.x in 19.99..280.01 && point.y in 19.99..280.01
        })
        val zoomed = SceneProjection.fit(camera.zoomBy(2.0), mesh.bounds, 300f, 300f, 20f)
        assertEquals(fit.scale * 2.0, zoomed.scale, 1e-8)
        assertEquals(fit.position(mesh.bounds.center), zoomed.position(mesh.bounds.center))
    }

    @Test fun malformedOrFutureShapesDoNotBreakTheRestOfTheScene() {
        val unknown = ExtensionSceneShape.Unknown("future")
        assertNull(SceneMesh.from(listOf(unknown, box.copy(top = 0.0))))
        assertNotNull(SceneMesh.from(listOf(unknown, box)))
    }
}
