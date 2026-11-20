package persona.feature.knowledge

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.toggleableState
import androidx.compose.ui.state.ToggleableState
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.*
import persona.core.contracts.parseTime
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons
import java.net.URI

private val projectFields = listOf("summary", "one_line")

/** Only fields with a review gap or missing coverage need a line on the ordinary project list and detail. */
@Composable
internal fun ProjectCoverageNotice(entry: KnowledgeEntry) {
    val knowledge = entry.projectKnowledge?.takeIf { entry.isProject } ?: return
    val lines = projectFields.mapNotNull { key ->
        val field = knowledge.fields[key]
        val summary = key == "summary"
        when {
            field?.status == "needs_review" -> {
                val label = stringResource(if (summary) R.string.knowledge_evidence_summary_review else R.string.knowledge_evidence_overview_review)
                if (field.uncovered > 0) pluralStringResource(R.plurals.knowledge_evidence_newer, field.uncovered, label, field.uncovered) else label
            }
            field?.status != "current" || field.cutoff == null -> stringResource(if (summary) R.string.knowledge_evidence_summary_unknown else R.string.knowledge_evidence_overview_unknown)
            else -> null
        }
    }
    if (lines.isNotEmpty() || knowledge.needsReview) {
        Text(if (lines.isEmpty()) stringResource(R.string.knowledge_evidence_review) else lines.joinToString("\n"),
            style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
    }
}

internal fun LazyListScope.projectEvidence(
    detail: KnowledgeDetail, open: Boolean, onToggle: () -> Unit, onEdit: () -> Unit,
    onEntry: (KnowledgeEntry) -> Unit, onWeb: (String) -> Unit,
) {
    val knowledge = detail.projectKnowledge?.takeIf { detail.kind == KnowledgeKind.Projects.wire } ?: return
    item("project-evidence") {
        JGroupItem(GroupPosition.Single) {
            JRow(stringResource(R.string.knowledge_evidence_title), Modifier.foldToggle(open, onToggle), titleLines = Int.MAX_VALUE,
                subtitle = if (knowledge.policy == "reference_only") stringResource(R.string.knowledge_evidence_reference_only) else null,
                trailing = { JIcon(if (open) PersonaIcons.collapse else PersonaIcons.expand, null, decorative = true) })
        }
    }
    if (!open) return
    itemsIndexed(projectFields, key = { _, key -> "project-coverage-$key" }) { index, key ->
        val field = knowledge.fields[key] ?: ProjectFieldCoverage()
        JGroupItem(GroupPosition.of(index, projectFields.size)) {
            val lines = buildList {
                add(field.cutoff?.let { cutoff -> pluralStringResource(R.plurals.knowledge_evidence_cutoff, knowledge.position, cutoff, knowledge.position) }
                    ?: stringResource(R.string.knowledge_evidence_cutoff_unknown))
                if (field.locked) add(stringResource(R.string.knowledge_evidence_locked))
                add(attribution(field.writer, maintainer = false))
                add(attribution(field.maintainer, maintainer = true))
                if (field.sourceRefs.isNotEmpty()) add(stringResource(R.string.knowledge_evidence_based_on, field.sourceRefs.joinToString("\n")))
            }
            JRow(stringResource(if (key == "summary") R.string.knowledge_evidence_summary else R.string.knowledge_evidence_overview),
                subtitle = lines.joinToString("\n"), titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE)
        }
    }
    if (knowledge.sources.isNotEmpty()) {
        item("project-sources") { JSectionHeader(stringResource(R.string.knowledge_evidence_sources)) }
        itemsIndexed(knowledge.sources, key = { _, source -> "project-source-${source.ref}" }) { index, source ->
            val ref = source.ref
            val linked = refEntry(ref, detail.links.orEmpty())
            val action: (() -> Unit)? = when {
                linked != null -> { { onEntry(linked) } }
                isWebRef(ref) -> { { onWeb(ref) } }
                else -> null
            }
            val locale = currentLocale
            val time = remember(source.stateAt, locale) {
                source.stateAt?.let(::parseTime)?.let { LocalizedFormats.format(it, LocalizedFormats.DATE_TIME, locale) }
            }
            val info = buildList {
                if (linked != null && linked.name != ref) add(ref)
                source.state?.takeIf(String::isNotBlank)?.let { state ->
                    val name = when (state) {
                        "open" -> stringResource(R.string.knowledge_evidence_open)
                        "closed" -> stringResource(R.string.knowledge_evidence_closed)
                        "merged" -> stringResource(R.string.knowledge_evidence_merged)
                        else -> state
                    }
                    add(stringResource(R.string.knowledge_evidence_source_status, name))
                    if (time != null) add(time)
                }
            }
            JGroupItem(GroupPosition.of(index, knowledge.sources.size)) {
                if (action != null) JRow(linked?.name ?: ref, subtitle = info.joinToString("\n").ifEmpty { null },
                    onClick = action, titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE)
                else SelectionContainer { JRow(ref, subtitle = info.joinToString("\n").ifEmpty { null }, titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE) }
            }
        }
        if (knowledge.sources.any { !it.state.isNullOrBlank() }) item("project-verification") { Note(stringResource(R.string.knowledge_evidence_verification)) }
    }
    if (knowledge.policy == "reference_only" || knowledge.policy == "managed") item("project-storage") {
        JGroupItem(GroupPosition.Single) {
            val referenceOnly = knowledge.policy == "reference_only"
            JRow(stringResource(if (referenceOnly) R.string.knowledge_evidence_reference_only else R.string.knowledge_evidence_managed),
                subtitle = stringResource(if (referenceOnly) R.string.knowledge_evidence_reference_hint else R.string.knowledge_evidence_managed_hint),
                titleLines = Int.MAX_VALUE, subtitleLines = Int.MAX_VALUE)
            JRow(stringResource(R.string.knowledge_evidence_edit), leadingIcon = PersonaIcons.edit, onClick = onEdit)
        }
    }
}

@Composable
private fun attribution(who: String?, maintainer: Boolean): String = when {
    who == "user" -> stringResource(if (maintainer) R.string.knowledge_evidence_maintainer_user else R.string.knowledge_evidence_writer_user)
    who == "pipeline" -> stringResource(if (maintainer) R.string.knowledge_evidence_maintainer_persona else R.string.knowledge_evidence_writer_persona)
    who == "external" && maintainer -> stringResource(R.string.knowledge_evidence_maintainer_external)
    who?.startsWith("ext:") == true && who.length > 4 ->
        stringResource(if (maintainer) R.string.knowledge_evidence_maintainer_extension else R.string.knowledge_evidence_writer_extension, who.substring(4))
    else -> stringResource(if (maintainer) R.string.knowledge_evidence_maintainer_unknown else R.string.knowledge_evidence_writer_unknown)
}

/** A field path such as projects/orchard/log identifies evidence without opening another project. */
private fun refEntry(ref: String, links: List<KnowledgeEntry>): KnowledgeEntry? {
    val parts = ref.split('/', limit = 3)
    if (parts.size != 2 || parts[1].isBlank() || parts[1] in setOf(".", "..")) return null
    val kind = KnowledgeKind.fromWire(parts[0]) ?: return null
    return links.firstOrNull { it.kind == kind.wire && it.id == parts[1] } ?: KnowledgeEntry(kind.wire, parts[1], ref)
}

private fun isWebRef(ref: String): Boolean = runCatching { URI(ref) }.getOrNull()?.let {
    it.scheme?.lowercase() in setOf("http", "https") && !it.host.isNullOrBlank() && it.userInfo == null
} == true

@Composable
internal fun ProjectStorageEditor(detail: KnowledgeDetail, work: KnowledgeTagging, onSave: (KnowledgeUpdate) -> Unit, onDismiss: () -> Unit) {
    val originalPolicy = rememberSaveable { detail.projectKnowledge?.policy.orEmpty() }
    // Linked evidence in project_knowledge.sources has its own relationship. Only these explicit pointers are editable.
    val originalRefs = rememberSaveable { detail.meta.strings("source_refs").joinToString("\n") }
    var policy by rememberSaveable { mutableStateOf(originalPolicy) }
    var text by rememberSaveable { mutableStateOf(originalRefs) }
    val changedRefs = if (text == originalRefs) null else text.lineSequence().map(String::trim).filter(String::isNotEmpty)
        .toList().takeUnless { it.joinToString("\n") == originalRefs }
    val update = KnowledgeUpdate(storagePolicy = policy.takeIf { it != originalPolicy }, sourceRefs = changedRefs)
    JSheet(onDismiss, title = stringResource(R.string.knowledge_evidence_edit), dismissible = !work.busy, scrollable = true) {
        Column(Modifier.fillMaxWidth().imePadding(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
            JSwitchRow(stringResource(R.string.knowledge_evidence_reference_only), policy == "reference_only", { policy = if (it) "reference_only" else "managed" },
                Modifier.testTag("project-storage-policy").semantics { toggleableState = ToggleableState(policy == "reference_only") },
                subtitle = stringResource(R.string.knowledge_evidence_reference_hint), subtitleLines = Int.MAX_VALUE, enabled = !work.busy)
            JTextField(text, { text = it }, Modifier.fillMaxWidth().testTag("project-source-refs"), label = stringResource(R.string.knowledge_evidence_refs),
                supportingText = stringResource(R.string.knowledge_evidence_refs_hint), minLines = 3, enabled = !work.busy)
            work.error?.let { JNotice(stringResource(R.string.knowledge_not_saved, it.text()), status = Status.NeedsYou) }
            JButton(stringResource(R.string.knowledge_save), { onSave(update) }, Modifier.fillMaxWidth(),
                enabled = update.storagePolicy != null || update.sourceRefs != null, loading = work.busy, loadingText = stringResource(R.string.knowledge_saving))
            JButton(stringResource(R.string.knowledge_evidence_cancel), onDismiss, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, enabled = !work.busy)
        }
    }
}
