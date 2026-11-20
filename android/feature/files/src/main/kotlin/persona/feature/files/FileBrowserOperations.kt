package persona.feature.files

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.job
import kotlinx.coroutines.isActive
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PersonaIcons

private class FileNameQuestion(val name: String, val count: Int, val answer: CompletableDeferred<SessionFileConflict?>)

internal suspend fun <T> onFileConflict(name: String, ask: suspend (String) -> SessionFileConflict?,
    action: suspend (SessionFileConflict?) -> T): T? {
    try { return action(null) }
    catch (error: Exception) {
        if (error is CancellationException || hubCodedFailure(error)?.let { it.status == 409 && it.code == "exists" } != true) throw error
        val taken = (editRefusal(error, name) as? EditRefusal.Exists)?.name?.takeIf(String::isNotBlank) ?: name
        return ask(taken)?.let { action(it) }
    }
}

internal fun archiveName(targets: List<FileBrowserTarget>, default: String, downloading: Boolean): String {
    val entry = targets.singleOrNull()?.entry ?: return default
    val stem = if (downloading || entry.dir) entry.name else entry.name.substringBeforeLast('.').ifEmpty { entry.name }
    return "$stem.zip"
}

private data class Destination(val move: Boolean, val targets: List<FileBrowserTarget>)

@Composable
internal fun rememberFileBrowserActions(controller: FilesController, folder: String, readOnly: Boolean, machineOs: String?,
    onOpenFolderTerminal: ((String) -> Unit)?): FileBrowserActions {
    val context = LocalContext.current
    val resources = LocalResources.current
    val feedback = LocalAppFeedback.current
    val scope = rememberCoroutineScope()
    val download = rememberFileDownload(controller)
    val api = controller.api
    val mid = controller.machineId
    val cwd = controller.cwd
    var creatingFolder by rememberSaveable(mid, cwd, folder) { mutableStateOf<Boolean?>(null) }
    var uploadFolder by rememberSaveable(mid, cwd) { mutableStateOf<String?>(null) }
    var destination by remember(controller, folder) { mutableStateOf<Destination?>(null) }
    var permissions by remember(controller, folder) { mutableStateOf<FileBrowserTarget?>(null) }
    val questions = remember(controller) { mutableStateListOf<FileNameQuestion>() }
    val transfers = remember(controller) { mutableStateListOf<FileTransferWork>() }
    FileTransferProgress(transfers.firstOrNull()?.takeIf { questions.isEmpty() })
    fun offers(feature: SessionFileFeature) = api.supports(mid, feature)
    fun text(id: Int) = resources.getString(id)
    fun refresh() { controller.directory() }
    fun success(message: String) { feedback?.notice(message, FeedbackStyle.Success) }
    fun runChange(name: String?, progress: Int? = null, change: suspend () -> Unit) {
        scope.launch {
            val work = progress?.let { FileTransferWork(text(it), currentCoroutineContext().job) }
            work?.let { transfers += it }
            try { change() }
            catch (cancelled: CancellationException) {
                if (progress != null && scope.isActive) refresh()
                throw cancelled
            }
            catch (error: Exception) {
                if (progress != null) refresh()
                val message = when (error) {
                    is UploadTooLarge -> text(R.string.files_action_too_large)
                    is UploadDocumentUnreadable -> text(R.string.files_action_upload_unreadable)
                    else -> editRefusalText(resources, editRefusal(error, name))
                }
                feedback?.notice(message, FeedbackStyle.Error)
            } finally { work?.let { transfers.remove(it) } }
        }
    }
    suspend fun ask(name: String, count: Int = 1): SessionFileConflict? = withContext(Dispatchers.Main.immediate) {
        val prompt = FileNameQuestion(name, count, CompletableDeferred())
        questions += prompt
        try { prompt.answer.await() } finally { questions.remove(prompt) }
    }
    val receiveUploads: (List<Uri>) -> Unit = { uris ->
        val dir = uploadFolder
        uploadFolder = null
        if (dir != null && controller.canWrite(dir) && uris.isNotEmpty()) runChange(null, R.string.files_action_uploading) {
            var sent = 0
            try {
                for (uri in uris.distinct()) {
                    val result = withUploadDocument(context, uri) { name, staged ->
                        onFileConflict(name, { ask(it) }) { choice -> api.uploadFile(mid, cwd, dir, name, staged, choice) }
                    }
                    if (result != null) sent++
                }
            } finally {
                if (sent > 0) {
                    refresh()
                    success(resources.getQuantityString(R.plurals.files_action_uploaded, sent, sent))
                }
            }
        }
    }
    val upload = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments(), receiveUploads)
    val photos = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(), receiveUploads)
    fun pickUpload(media: Boolean) {
        if (uploadFolder != null) return
        uploadFolder = folder
        try {
            if (media) photos.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo))
            else upload.launch(arrayOf("*/*"))
        } catch (error: android.content.ActivityNotFoundException) {
            uploadFolder = null
            feedback?.notice(text(R.string.files_action_upload_unreadable), FeedbackStyle.Error)
        }
    }
    fun moveOrCopy(action: Destination, dir: String) {
        if (!controller.canWrite(dir) || (action.move && action.targets.any { !controller.canWrite(it.path) })) return
        val paths = action.targets.map { it.path }
        val name = action.targets.first().entry.name
        runChange(name, if (action.move) R.string.files_action_moving else R.string.files_action_copying) {
            val result = onFileConflict(name, { ask(it, paths.size) }) { choice ->
                if (action.move) api.moveFiles(mid, cwd, paths, dir, choice) else api.copyFiles(mid, cwd, paths, dir, choice)
            }
            if (result != null) {
                refresh()
                success(resources.getQuantityString(if (action.move) R.plurals.files_action_moved else R.plurals.files_action_copied, result.size, result.size))
            }
        }
    }
    fun archive(targets: List<FileBrowserTarget>, downloading: Boolean) {
        val name = archiveName(targets, text(R.string.files_action_archive_name), downloading)
        runChange(name, R.string.files_action_compressing) {
            val result = api.zipFiles(mid, cwd, targets.map { it.path }, name, if (downloading) null else folder)
            if (downloading) download(result.path, result.name, "application/zip") else {
                if (controller.state.value.here == folder) controller.showInFolder(result.path) else refresh()
                success(resources.getString(R.string.files_action_compressed, result.name))
            }
        }
    }
    fun safeTargets(targets: List<FileBrowserTarget>) = !readOnly && targets.all { controller.canWrite(it.path) }
    fun selected(targets: List<FileBrowserTarget>): List<JAction> = buildList {
        if (targets.isEmpty()) return@buildList
        val single = targets.singleOrNull()?.takeUnless { it.entry.dir }
        if (targets.none { it.entry.linkBroken } && (single != null || offers(SessionFileFeature.Zip))) {
            add(JAction(text(R.string.files_action_download), PersonaIcons.down) {
                if (single == null) archive(targets, downloading = true) else download(single.path, single.entry.name, null)
            })
        }
        if (offers(SessionFileFeature.Copy)) add(JAction(text(R.string.files_action_copy_to), PersonaIcons.copy) { destination = Destination(false, targets) })
        if (safeTargets(targets)) {
            if (offers(SessionFileFeature.Edit)) add(JAction(text(R.string.files_action_move_to), PersonaIcons.folder) { destination = Destination(true, targets) })
            if (offers(SessionFileFeature.Zip)) add(JAction(text(R.string.files_action_compress), PersonaIcons.archive) { archive(targets, downloading = false) })
        }
    }
    fun permission(target: FileBrowserTarget): JAction? =
        if (!readOnly && controller.canWrite(target.path) && machineOs?.lowercase() !in setOf("windows", "win32") &&
            !target.entry.symlink && offers(SessionFileFeature.Chmod) && offers(SessionFileFeature.Info))
            JAction(text(R.string.files_action_permissions), PersonaIcons.locked) { permissions = target } else null
    fun terminal(path: String): JAction? = onOpenFolderTerminal?.takeIf { offers(SessionFileFeature.PaneCwd) }?.let { open ->
        JAction(text(R.string.files_action_terminal), PersonaIcons.terminal) { open(path) }
    }

    creatingFolder?.let { isFolder ->
        NewFileName(isFolder, onDismiss = { creatingFolder = null }) { name ->
            creatingFolder = null
            runChange(name) {
                if (isFolder) {
                    val path = api.mkdir(mid, cwd, folder, name)
                    if (controller.state.value.here == folder) controller.showInFolder(path) else refresh()
                } else {
                    val created = api.writeText(mid, cwd, joinRemotePath(folder, name), "", create = true)
                    refresh()
                    if (controller.state.value.here == folder) controller.openEditor(created.path)
                }
            }
        }
    }
    destination?.let { action ->
        DestinationFolderPicker(controller, folder, action, onDismiss = { destination = null }) { dir ->
            destination = null
            moveOrCopy(action, dir)
        }
    }
    permissions?.let { FilePermissions(controller, it) { permissions = null } }
    questions.firstOrNull()?.let { question ->
        val dismiss = { question.answer.complete(null); Unit }
        JDialog(dismiss, title = stringResource(R.string.files_action_conflict, question.name),
            text = if (question.count > 1) stringResource(R.string.files_action_bulk_conflict, question.count) else null,
            confirm = stringResource(R.string.files_action_keep) to { question.answer.complete(SessionFileConflict.Keep) }) {
            JButton(stringResource(R.string.files_action_replace), { question.answer.complete(SessionFileConflict.Replace) },
                kind = JButtonKind.Destructive)
        }
    }
    return FileBrowserActions(
        newItems = buildList {
            if (!readOnly && controller.canWrite(folder)) {
                if (offers(SessionFileFeature.Mkdir)) add(JAction(text(R.string.files_action_new_folder), PersonaIcons.folder) { creatingFolder = true })
                if (offers(SessionFileFeature.Write)) add(JAction(text(R.string.files_action_new_text), PersonaIcons.fileText) { creatingFolder = false })
                if (offers(SessionFileFeature.Upload)) {
                    add(JAction(text(R.string.files_action_upload_photos), PersonaIcons.fileImage) { pickUpload(true) })
                    add(JAction(text(R.string.files_action_upload), PersonaIcons.queueUp) { pickUpload(false) })
                }
            }
        },
        folderItems = listOfNotNull(terminal(folder)),
        rowItems = { target -> buildList {
            addAll(selected(listOf(target)))
            if (safeTargets(listOf(target)) && offers(SessionFileFeature.Copy)) add(JAction(text(R.string.files_action_duplicate), PersonaIcons.copy) {
                val parent = FilesPaths.parent(target.path) ?: folder
                runChange(target.entry.name, R.string.files_action_copying) {
                    val changes = api.copyFiles(mid, cwd, listOf(target.path), parent, SessionFileConflict.Keep)
                    refresh()
                    success(resources.getQuantityString(R.plurals.files_action_copied, changes.size, changes.size))
                }
            })
            permission(target)?.let(::add)
            if (target.entry.dir && !target.entry.linkBroken) terminal(target.path)?.let(::add)
        } },
        selectedItems = ::selected,
        permissions = ::permission,
    )
}

@Composable
private fun NewFileName(folder: Boolean, onDismiss: () -> Unit, onCreate: (String) -> Unit) {
    val suggested = if (folder) "" else stringResource(R.string.files_action_untitled)
    var name by rememberSaveable(folder) { mutableStateOf(suggested) }
    val problem = if ('\\' in name) NameProblem.Slash else nameProblem(name)
    JDialog(onDismiss, title = stringResource(if (folder) R.string.files_action_new_folder else R.string.files_action_new_text),
        confirmEnabled = problem == null, confirm = stringResource(R.string.files_action_create) to { onCreate(name.trim()) }) {
        JTextField(name, { name = it }, Modifier.fillMaxWidth().testTag("files-create-name"), label = stringResource(R.string.files_rename_name),
            singleLine = true, autofill = false)
        if (problem != null && name.isNotBlank()) Text(nameProblemText(LocalResources.current, problem), color = PersonaTheme.palette.danger.text)
    }
}

@Composable
private fun DestinationFolderPicker(controller: FilesController, initial: String, action: Destination,
    onDismiss: () -> Unit, choose: (String) -> Unit) {
    val resources = LocalResources.current
    var here by rememberSaveable(initial) { mutableStateOf(initial) }
    var listing by remember(here) { mutableStateOf<SessionDirectory?>(null) }
    var error by remember(here) { mutableStateOf<String?>(null) }
    var newFolder by rememberSaveable(here) { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    var attempt by remember(here) { mutableIntStateOf(0) }
    LaunchedEffect(controller, here, attempt) {
        error = null
        try { listing = controller.api.directory(controller.machineId, controller.cwd, here) }
        catch (cancelled: CancellationException) { throw cancelled }
        catch (failure: Exception) { error = filesLoadError(resources, failure) }
    }
    val shown = listing?.path?.ifBlank { here } ?: here
    if (newFolder) {
        NewFileName(folder = true, onDismiss = { newFolder = false }) { name ->
            newFolder = false
            scope.launch {
                try { here = controller.api.mkdir(controller.machineId, controller.cwd, shown, name) }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { error = editRefusalText(resources, editRefusal(failure, name)) }
            }
        }
        return
    }
    val nested = action.targets.any { it.entry.dir && FilesPaths.isWithin(shown, it.path) }
    val same = action.move && action.targets.all { FilesPaths.parent(it.path) == shown }
    JSheet(onDismiss, title = stringResource(if (action.move) R.string.files_action_move_to else R.string.files_action_copy_to)) {
        Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
            Text(shown, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
            FilesPaths.parent(shown)?.let { up -> JRow(stringResource(R.string.files_up), leadingIcon = PersonaIcons.queueUp, onClick = { here = up }) }
            if (listing != null && controller.canWrite(shown) && controller.api.supports(controller.machineId, SessionFileFeature.Mkdir)) {
                JTextButton(stringResource(R.string.files_action_new_folder), { newFolder = true }, icon = PersonaIcons.folder)
            }
            when {
                error != null -> {
                    Text(requireNotNull(error), color = PersonaTheme.palette.danger.text)
                    JButton(stringResource(R.string.files_retry), { attempt++ }, kind = JButtonKind.Quiet)
                }
                listing == null -> androidx.compose.material3.CircularProgressIndicator()
                else -> LazyColumn(Modifier.weight(1f, fill = false).fillMaxWidth()) {
                    items(listing!!.entries.filter { it.dir && !it.linkBroken }, key = { it.name }) { entry ->
                        JRow(entry.name, leadingIcon = PersonaIcons.folder, onClick = { here = joinRemotePath(shown, entry.name) })
                    }
                    if (listing!!.truncated) item { Text(stringResource(R.string.files_too_many, listing!!.entries.size), style = PersonaTheme.type.callout) }
                }
            }
            JButton(stringResource(if (action.move) R.string.files_action_move_here else R.string.files_action_copy_here), { choose(shown) },
                enabled = listing != null && controller.canWrite(shown) && !nested && !same)
        }
    }
}
