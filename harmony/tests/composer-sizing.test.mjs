import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';

const source = readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets', import.meta.url), 'utf8');
const tokens = readFileSync(new URL('../entry/src/main/ets/design/Tokens.ets', import.meta.url), 'utf8');
const Tokens = new Function('return (' + stripTypeScriptTypes(tokens.replace('export class', 'class')) + ')')();
function method(name) {
  const match = new RegExp('  private ' + name + '\\(').exec(source);
  assert.ok(match, name);
  const start = match.index, body = source.indexOf('{', start);
  let depth = 1, i = body + 1;
  for (; i < source.length && depth; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') depth--;
  }
  assert.equal(depth, 0, name);
  return source.slice(start, i);
}
const Composer = new Function('Tokens', 'Edge', 'return ' + stripTypeScriptTypes('class Composer {' +
  ['hasComposerAction', 'trailingActionWidth', 'leftActionWidth', 'expandedComposer', 'composerColumns', 'sendColumns',
    'leftColumns', 'verticalActions', 'maxControlsHeight', 'inlineEditorMaxHeight', 'revealOverflowingActions'].map(method).join('\n') + '}'))(Tokens, { Bottom: 'bottom' });
function fixture(density = 3, width = 372) {
  return Object.assign(new Composer(), {
    composerWidth: width, fieldFocused: false, primaryStop: () => false, sending: false, model: { demo: false, draft: '' },
    attachments: { attachments: [] }, environment: { fontScale: 1, keyboardHeightPx: 0 }, showContextMenu: true,
    availableHeight: 800, editorHeight: 0, controlsHeight: 80,
    getUIContext: () => ({ vp2px: value => value * density })
  });
}

test('compact Persona and Agent editors get the remaining width after fixed actions and equal gaps', () => {
  const v = fixture();
  assert.equal(v.expandedComposer(), false);
  assert.equal(v.composerColumns(), 1116);
  assert.equal(v.leftColumns(), 168);
  assert.equal(v.sendColumns(), 168);
  assert.equal(v.composerColumns() - v.leftColumns() - v.sendColumns(), 780);
  v.showContextMenu = false;
  assert.equal(v.leftColumns(), 168);
  assert.equal(v.composerColumns() - v.leftColumns() - v.sendColumns(), 780);
});

test('compact action gaps stay within one physical pixel at each native device density', () => {
  for (const density of [3, 2.75, 3.125]) for (const width of [372, 587.27, 647.68]) {
    const v = fixture(density, width), unit = width * density / v.composerColumns();
    assert.equal(v.expandedComposer(), false);
    for (const gap of [v.leftColumns() * unit - v.leftActionWidth() * density,
      v.sendColumns() * unit - Tokens.touch * density]) {
      assert.ok(Math.abs(gap - Tokens.gap * density) <= 1.01, { density, width, gap });
    }
  }
});

test('focus, a restored draft, a quote or an attachment opens the editing card independently', () => {
  for (const change of [
    v => { v.fieldFocused = true; },
    v => { v.model.draft = '你好'; },
    v => { v.model.sourceContext = { title: '引用' }; },
    v => { v.attachments.attachments.push({ id: 'file' }); }]) {
    const v = fixture();
    change(v);
    assert.equal(v.expandedComposer(), true);
    assert.equal(v.composerColumns(), 12);
  }
});

test('a blurred empty composer returns to one line, with enough space for the keyboard and both toolbars', () => {
  const v = fixture();
  v.fieldFocused = true;
  assert.equal(v.expandedComposer(), true);
  v.fieldFocused = false;
  assert.equal(v.expandedComposer(), false);

  // The iPhone keeps the unedited attachment / field / microphone row compact at AX5.
  for (const scale of [1,1.5,3.2]) for (const width of [300,372]) {
    const phone=fixture(3,width); phone.environment.fontScale=scale;
    assert.equal(phone.expandedComposer(),false);
    assert.equal(phone.composerColumns(),width*3);
  }
  const narrow=fixture(3,220);
  assert.equal(narrow.expandedComposer(),true);
  assert.equal(narrow.composerColumns(),12);

});

test('a draft can expand above the keyboard, while a short unfocused draft keeps its quiet shape', () => {
  const Editor = new Function('Tokens','return '+stripTypeScriptTypes('class Editor {'+
    ['offersDraftEditor','rememberSelection','parkSelection','restoreSelection','draftEditorCurrent'].map(method).join('\n')+'}'))(Tokens);
  const v = Object.assign(new Editor(), {fieldFocused:false, model:{draft:'Review PR 42'}, editorHeight:48,
    environment:{fontScale:1}, draftEditorOwner:'hubA/review', sendCurrent:()=>true,composerAvailable:()=>true,
    composerOwnerKey:()=>'hubA/review'});
  assert.equal(v.offersDraftEditor(),false);v.fieldFocused=true;assert.equal(v.offersDraftEditor(),true);
  v.fieldFocused=false;v.model.draft='a\nb';assert.equal(v.offersDraftEditor(),true);
  v.model.draft='wrapped';v.editorHeight=167;assert.equal(v.offersDraftEditor(),true);
  v.rememberSelection(1,6);v.parkSelection();v.rememberSelection(0,0);
  v.model.draft='abc';let restored;
  v.restoreSelection({setTextSelection:(...range)=>{restored=range;}});
  assert.deepEqual(restored,[1,3]);assert.equal(v.draftEditorCurrent(),true);
  v.composerOwnerKey=()=>'hubB/review';assert.equal(v.draftEditorCurrent(),false);
});

test('resized phone viewport leaves a full keyboard and send target at both text sizes', () => {
  for (const scale of [1, 3.2]) for (const width of [242, 356, 372, 620]) {
    const v = fixture(3, width);
    v.fieldFocused = true;
    v.model.draft = 'Review PR 42\n'.repeat(20);
    v.environment.fontScale = scale;
    v.availableHeight = 270;
    v.environment.keyboardHeightPx = 1054;
    assert.equal(v.maxControlsHeight(), 270); // ArkUI has already removed the keyboard.
    assert.equal(v.verticalActions(), width < 260);
    const actionRows = v.verticalActions() ? 2 : 1;
    const reserved = actionRows * Tokens.touch + Tokens.gap + Tokens.footer + 8;
    const height = v.inlineEditorMaxHeight();
    assert.ok(height + reserved <= v.availableHeight);
    const lines = (height - 2 * Tokens.inset) / (Tokens.lineHeight * scale);
    assert.ok(lines >= 1 && lines <= 6 && Math.abs(lines - Math.round(lines)) < 1e-7);
    if (!v.verticalActions()) {
      const middle = (12 - v.leftColumns() - v.sendColumns()) * width / 12;
      assert.ok(middle >= 44, 'keyboard button fits between leading tools and send');
    }
    v.environment.keyboardHeightPx = 0;
    v.availableHeight = 690;
    assert.equal(v.maxControlsHeight(), 552);
    assert.ok(v.inlineEditorMaxHeight() >= height);
    assert.equal(v.model.draft, 'Review PR 42\n'.repeat(20));
  }
});

test('quotes, attachments and the dock reserve their measured space before growing the editor', () => {
  const v = fixture();
  Object.assign(v, { fieldFocused: true, availableHeight: 300, editorHeight: 170, controlsHeight: 310 });
  Object.assign(v.environment, { fontScale: 3.2, keyboardHeightPx: 1024 });
  assert.equal(v.inlineEditorMaxHeight(), 2 * Tokens.inset + Tokens.lineHeight * 3.2);
  v.availableHeight = 125;
  assert.equal(v.inlineEditorMaxHeight(), Tokens.touch);
  let scrolls = 0;
  v.controlsScroller = { scrollEdge(side) { assert.equal(side, 'bottom'); scrolls++; } };
  v.active = true;
  v.revealOverflowingActions();
  assert.equal(scrolls, 1);
  v.draftEditorOpen = true;
  v.revealOverflowingActions();
  v.draftEditorOpen = false;
  v.environment.keyboardHeightPx = 0;
  v.revealOverflowingActions();
  assert.equal(scrolls, 1);
});

test('opening and collapsing the page editor restores its edited draft and selection without sending', () => {
  const Page = new Function('ConversationFrame', 'return ' + stripTypeScriptTypes('class Page {' +
    ['draftEditorCurrent', 'rememberSelection', 'parkSelection', 'restoreSelection', 'expandDraftEditor',
      'focusDraftPage', 'finishPageInput', 'draftPageGone'].map(method).join('\n') + '}'))(class {
    constructor(action) { this.onFrame = action; }
  });
  const focused = [], selected = [];
  const v = Object.assign(new Page(), {
    fieldFocused: true, composerOwnerKey: () => 'hub/review42', sendCurrent: () => true, composerAvailable: () => true,
    model: { draft: 'Review PR 42' }, sendDraft: () => assert.fail('editing must never submit'),
    inlineField: { stopEditing() {}, setTextSelection: (...range) => selected.push(range) },
    pageField: { stopEditing() {}, setTextSelection: (...range) => selected.push(range) },
    getUIContext: () => ({ postFrameCallback: frame => frame.onFrame(),
      getFocusController: () => ({ requestFocus: id => focused.push(id) }) })
  });
  v.rememberSelection(2, 5);
  v.expandDraftEditor();
  v.focusDraftPage();
  v.model.draft = '核对完成\n还可以编辑';
  v.rememberSelection(4, 6);
  v.finishPageInput();
  v.draftEditorOpen = false;
  v.draftPageGone();
  assert.equal(v.model.draft, '核对完成\n还可以编辑');
  assert.deepEqual(focused, ['chat.editor.input', 'chat.input']);
  assert.deepEqual(selected, [[2, 5], [4, 6]]);
});
