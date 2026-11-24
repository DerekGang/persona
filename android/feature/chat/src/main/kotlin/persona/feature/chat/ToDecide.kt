package persona.feature.chat

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.Saver
import androidx.compose.runtime.saveable.listSaver
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withTimeoutOrNull
import persona.core.contracts.Interaction
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.parseTime
import persona.core.navigation.DecisionSource
import persona.core.navigation.MessageJumps
import java.time.Instant

internal data class ToDecideCard(val request: Interaction, val task: Boolean, val title: String? = null) {
    val id: String get() = request.id
}

private val TaskDecisionKinds = setOf("question", "plan", "permission", "screen", "elicitation")

/** Preserve the conversation's own order and use stable oldest-first order for its tasks. */
internal fun toDecideCards(
    own: List<Interaction>, requests: List<Interaction>, taskKeys: Set<SessionKey>, taskTitle: (Interaction) -> String?,
): List<ToDecideCard> = (
    own.map { ToDecideCard(it, task = false) } + requests.asSequence()
        .filter { it.sessionKey in taskKeys && it.kind in TaskDecisionKinds }
        .sortedBy { parseTime(it.createdAt) ?: Instant.MIN }
        .map { ToDecideCard(it, task = true, title = taskTitle(it)) }
        .toList()
    ).distinctBy(ToDecideCard::id)

@Stable
internal class ToDecideState(current: String? = null, index: Int = 0, folded: Set<String> = emptySet()) {
    private var current by mutableStateOf(current)
    private var index by mutableIntStateOf(index)
    private var folded by mutableStateOf(folded)

    fun selected(ids: List<String>): Int? {
        if (ids.isEmpty()) return null
        return current?.let(ids::indexOf)?.takeIf { it >= 0 } ?: index.coerceIn(ids.indices)
    }

    fun select(ids: List<String>, index: Int) {
        val target = ids.getOrNull(index) ?: return
        current = target
        this.index = index
    }

    fun isCollapsed(ids: List<String>): Boolean = ids.any { it in folded }
    fun newSinceCollapse(ids: List<String>): Int = if (isCollapsed(ids)) ids.count { it !in folded } else 0
    fun collapse(ids: List<String>) { folded = ids.toSet() }
    fun show(ids: List<String>, id: String? = null) {
        folded = emptySet()
        if (id != null) select(ids, ids.indexOf(id))
    }

    companion object {
        val Saver: Saver<ToDecideState, Any> = listSaver(
            save = { listOf(it.current.orEmpty(), it.index, ArrayList(it.folded)) },
            restore = { saved ->
                ToDecideState((saved[0] as String).ifEmpty { null }, saved[1] as Int,
                    (saved[2] as List<*>).filterIsInstance<String>().toSet())
            },
        )
    }
}

@Composable
internal fun rememberToDecideState(key: SessionKey) = rememberSaveable(key, saver = ToDecideState.Saver) { ToDecideState() }

internal enum class ToDecideSkipReason { Terminal, Permission, Plan }

internal fun Interaction.toDecideSkipReason(): ToDecideSkipReason? = when (kind) {
    "screen" -> if (dialog?.commandAsk != null) ToDecideSkipReason.Permission else ToDecideSkipReason.Terminal
    "permission" -> ToDecideSkipReason.Permission
    "plan" -> ToDecideSkipReason.Plan
    else -> null
}

internal fun toDecideDots(count: Int, selected: Int): IntRange {
    val slots = minOf(7, count)
    val start = (selected - slots / 2).coerceIn(0, count - slots)
    return start until (start + slots)
}

/** Find the message before the asking tool. The source chat's newest question is a terminal card's fallback. */
internal fun decisionSourceItem(items: List<Item>, toolUseId: String?, ownQuestion: String? = null): String? {
    val floor = if (toolUseId.isNullOrBlank()) (items.size - 400).coerceAtLeast(0) else 0
    val call = (items.lastIndex downTo floor).firstOrNull { index ->
        val item = items[index]
        item.kind == "tool_use" && when {
            !toolUseId.isNullOrBlank() -> item.toolUseId == toolUseId
            ownQuestion != null -> item.isQuestionCall() && item.firstQuestion() == ownQuestion
            else -> item.isQuestionCall()
        }
    } ?: return null
    return (call - 1 downTo 0).firstNotNullOfOrNull { index ->
        val item = items[index]
        item.id.takeIf { item.kind == "text" && item.toolUseId == null &&
            (item.role == "user" || item.role == "assistant" && !item.text.isNullOrBlank()) }
    }
}

/** Resolve the task's tool call once, then let the existing message-jump machinery scroll and highlight it. */
@Composable
internal fun JumpToDecisionSource(key: SessionKey, source: DecisionSource?, repository: ChatRepository) {
    var handled by rememberSaveable(key, source?.toolUseId) { mutableStateOf(false) }
    LaunchedEffect(key, source, repository) {
        if (source == null || handled) return@LaunchedEffect
        val snapshots = repository.observe(key)
        var page = snapshots.first { !it.loading && !it.loadingOlder }
        var item = decisionSourceItem(page.items, source.toolUseId)
        var pages = 0
        while (item == null && !source.toolUseId.isNullOrBlank() && page.hasOlder && pages < MESSAGE_JUMP_PAGE_LIMIT) {
            val first = page.items.firstOrNull()?.id
            try { repository.loadOlder(key) } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { break }
            page = withTimeoutOrNull(5_000) {
                snapshots.first { !it.loadingOlder && (!it.hasOlder || it.items.firstOrNull()?.id != first) }
            } ?: break
            item = decisionSourceItem(page.items, source.toolUseId)
            pages++
        }
        handled = true
        item?.let { MessageJumps.request(key.machineId, key.sessionId, it) }
    }
}

private fun Item.isQuestionCall(): Boolean = when (val name = toolName) {
    "AskUserQuestion", "ask_user", "request_user_input" -> true
    else -> name?.endsWith("__ask_user") == true
}

private fun Item.firstQuestion(): String? {
    val questions = (toolInput as? JsonObject)?.get("questions") as? JsonArray
    val first = questions?.firstOrNull() as? JsonObject
    return (first?.get("question") as? JsonPrimitive)?.takeIf { it.isString }?.content
}
