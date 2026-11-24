package persona.feature.chat

import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.SessionKey

class ToDecideTest {
    @Test fun ownCardsLeadThenTaskCardsAreOldestFirstWithoutDuplicates() {
        val own = card("own", "main", "permission", 9)
        val otherOwn = card("other", "main", "question", 1)
        val old = card("old", "task", "question", 3)
        val plan = card("plan", "task", "plan", 6)
        val tool = card("tool", "task", "permission", 6)
        val foreign = card("foreign", "unrelated", "question", 0)
        val cards = toDecideCards(listOf(own, otherOwn), listOf(tool, plan, old, foreign, own),
            setOf(SessionKey("host", "task"), SessionKey("host", "main"))) { "Example task" }
        assertEquals(listOf("own", "other", "old", "tool", "plan"), cards.map { it.id })
        assertFalse(cards.first().task)
        assertEquals("Example task", cards.last().title)
    }

    @Test fun arrivalsKeepTheShownCardAndAnswersAdvanceInPlace() {
        val state = ToDecideState()
        val original = listOf("one", "two", "three")
        state.select(original, 1)
        val new = listOf("new") + original
        assertEquals(2, state.selected(new))
        state.select(new, 2)
        assertEquals(2, state.selected(new - "two"))
        assertEquals(1, state.selected(listOf("new", "one")))
        assertNull(state.selected(emptyList()))
    }

    @Test fun collapseOutlastsArrivalsAndResetsWhenTheFoldedCardsLeave() {
        val state = ToDecideState()
        state.collapse(listOf("one", "two"))
        assertTrue(state.isCollapsed(listOf("two", "new")))
        assertEquals(1, state.newSinceCollapse(listOf("two", "new")))
        assertFalse(state.isCollapsed(listOf("new")))
        state.show(listOf("one", "two"), "two")
        assertFalse(state.isCollapsed(listOf("one", "two")))
        assertEquals(1, state.selected(listOf("one", "two")))
    }

    @Test fun skipReasonsAndDotWindowMatchTheCardKindAndPosition() {
        assertEquals(ToDecideSkipReason.Terminal, card("x", "main", "screen", 0).toDecideSkipReason())
        assertEquals(ToDecideSkipReason.Permission, card("y", "main", "permission", 0).toDecideSkipReason())
        assertEquals(ToDecideSkipReason.Plan, card("z", "main", "plan", 0).toDecideSkipReason())
        assertNull(card("q", "main", "question", 0).toDecideSkipReason())
        assertEquals(2..8, toDecideDots(9, 8))
        assertEquals(0..2, toDecideDots(3, 0))
    }

    @Test fun sourceFindsTheAnsweringTurnByIdOrByQuestion() {
        val items = listOf(
            Item("asked", role = "user", kind = "text", text = "Do the work"),
            Item("first", role = "assistant", kind = "text", text = "I need one detail"),
            question("call-one", "First?"),
            Item("second", role = "assistant", kind = "text", text = "One more detail"),
            question("call-two", "Second?"),
        )
        assertEquals("first", decisionSourceItem(items, "call-one"))
        assertEquals("second", decisionSourceItem(items, null))
        assertEquals("first", decisionSourceItem(items, null, ownQuestion = "First?"))
        assertNull(decisionSourceItem(items, "absent"))
    }

    private fun card(id: String, session: String, kind: String, second: Int): Interaction = Interaction(
        id = id, machineId = "host", sessionId = session, kind = kind, createdAt = "2026-01-01T00:00:0${second}Z",
    )

    private fun question(id: String, text: String): Item = Item(id, role = "assistant", kind = "tool_use", toolUseId = id,
        toolName = "ask_user", toolInput = buildJsonObject { put("questions", buildJsonArray { add(buildJsonObject { put("question", text) }) }) })
}
