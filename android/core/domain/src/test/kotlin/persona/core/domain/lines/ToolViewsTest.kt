package persona.core.domain.lines

import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.Item
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.ExtensionView

class ToolViewsTest {
    @Test fun readsItemAndFoldViewsAndKeepsThemInAClosedWorkGroup() {
        val first = JsonCodec.decodeFromString(Item.serializer(), """
            {"id":"result","seq":2,"kind":"tool_result","tool_views":[{"title":"Weekday route"}]}
        """.trimIndent())
        val folded = JsonCodec.decodeFromString(Item.serializer(), """
            {"id":"fold","seq":3,"kind":"fold","fold":{"tools":1,"tool_views":[{"title":"Weekend route"}]}}
        """.trimIndent())
        val tool = WorkItem.Tool(ToolCall(Item("use", kind = "tool_use").asLineItem(), first.asLineItem(), emptyList()))
        val fold = WorkItem.Folded(folded.asLineItem())
        val work = ChatLine.Work("work", listOf(tool, fold), live = false, start = null, end = null, open = false)
        assertEquals(listOf(ExtensionView(title = "Weekday route"), ExtensionView(title = "Weekend route")), work.toolViews())
        assertEquals(listOf(ExtensionView(title = "Weekend route")), ChatLine.Step(work.id, fold, false).toolViews())
        assertEquals(emptyList<ExtensionView>(), ChatLine.Row(ChatRow("reply", Item("reply", kind = "text").asLineItem())).toolViews())
    }
}
