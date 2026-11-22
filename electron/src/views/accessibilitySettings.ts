/**
 * Settings sections for the editor's look and for accessibility. Every
 * control writes to state/prefs.ts, which applies it at once.
 */

import { h } from '../lib/dom';
import { DEFAULT_PREFS, EDITOR_THEMES, MATCH_APP, usePrefsStore, type Prefs } from '../state/prefs';
import { useUiStore } from '../state/ui';

/** A labelled checkbox with a hint under it. */
function toggle(id: string, label: string, hint: string, key: keyof Prefs): HTMLElement {
  const box = h('input', { type: 'checkbox', id });
  box.checked = Boolean(usePrefsStore.getState()[key]);
  box.addEventListener('change', () => usePrefsStore.getState().setPref(key, box.checked as never));
  return h('div', { class: 'pref-check' }, h('label', { for: id }, box, ' ', label), h('p', { class: 'pref-hint', text: hint }));
}

/** Editor theme, font size, line spacing and the minimap: part of Appearance. */
export function renderEditorAppearance(): HTMLElement {
  const prefs = usePrefsStore.getState();
  const themeSelect = h('select', { id: 'pref-editor-theme' },
    h('option', { value: MATCH_APP, text: 'Match the app (light or dark)' }),
    h('optgroup', { label: 'Dark' }, ...EDITOR_THEMES.filter((t) => t.dark).map((t) => h('option', { value: t.id, text: t.label }))),
    h('optgroup', { label: 'Light' }, ...EDITOR_THEMES.filter((t) => !t.dark).map((t) => h('option', { value: t.id, text: t.label }))),
  );
  themeSelect.value = prefs.editorTheme;
  themeSelect.addEventListener('change', () => usePrefsStore.getState().setPref('editorTheme', themeSelect.value));

  // A few lines of code in the chosen theme, so the choice can be seen.
  const preview = h('pre', { class: 'code-theme-preview', 'aria-hidden': 'true' },
    h('span', { class: 'tok-comment', text: '# Check the token in constant time\n' }),
    h('span', { class: 'tok-keyword', text: 'def ' }), h('span', { class: 'tok-function', text: 'verify' }), h('span', { class: 'tok-operator', text: '(' }), 'token', h('span', { class: 'tok-operator', text: ': ' }), h('span', { class: 'tok-type', text: 'str' }), h('span', { class: 'tok-operator', text: ') -> ' }), h('span', { class: 'tok-type', text: 'bool' }), h('span', { class: 'tok-operator', text: ':\n' }),
    '    ', h('span', { class: 'tok-keyword', text: 'return ' }), 'hmac', h('span', { class: 'tok-operator', text: '.' }), h('span', { class: 'tok-function', text: 'compare_digest' }), h('span', { class: 'tok-operator', text: '(' }), 'token', h('span', { class: 'tok-operator', text: ', ' }), h('span', { class: 'tok-string', text: '"s3cret"' }), h('span', { class: 'tok-operator', text: ') ' }), h('span', { class: 'tok-keyword', text: 'and ' }), h('span', { class: 'tok-number', text: '42\n' }),
  );

  const size = h('input', { type: 'range', id: 'pref-editor-size', min: '10', max: '24', step: '1', value: String(prefs.editorFontSize) });
  const sizeValue = h('output', { for: 'pref-editor-size', text: `${prefs.editorFontSize}px` });
  size.addEventListener('input', () => {
    sizeValue.textContent = `${size.value}px`;
    usePrefsStore.getState().setPref('editorFontSize', Number(size.value));
  });

  const spacing = h('select', { id: 'pref-editor-spacing' },
    h('option', { value: 'compact', text: 'Compact' }),
    h('option', { value: 'normal', text: 'Normal' }),
    h('option', { value: 'relaxed', text: 'Relaxed' }),
  );
  spacing.value = prefs.editorLineSpacing;
  spacing.addEventListener('change', () => usePrefsStore.getState().setPref('editorLineSpacing', spacing.value as Prefs['editorLineSpacing']));

  return h('div', { class: 'pref-group' },
    h('h3', { class: 'pref-subtitle', text: 'Editor' }),
    h('div', { class: 'pref-form' },
      h('label', { for: 'pref-editor-theme', text: 'Editor theme' }), themeSelect,
      h('label', { for: 'pref-editor-size', text: 'Font size' }), h('span', { class: 'pref-range' }, size, sizeValue),
      h('label', { for: 'pref-editor-spacing', text: 'Line spacing' }), spacing,
    ),
    preview,
    toggle('pref-minimap', 'Show the minimap', 'A see-through outline of the whole file over the right edge of the editor; findings show in their colour. Click or drag it to scroll.', 'minimap'),
  );
}

/** The Accessibility section. */
export function renderAccessibility(rerender: () => void): HTMLElement {
  const prefs = usePrefsStore.getState();
  const ui = useUiStore.getState();

  const motion = h('select', { id: 'pref-motion' },
    h('option', { value: 'system', text: 'Follow the system setting' }),
    h('option', { value: 'reduce', text: 'Always reduce motion' }),
  );
  motion.value = prefs.motion === 'reduce' ? 'reduce' : 'system';
  motion.addEventListener('change', () => usePrefsStore.getState().setPref('motion', motion.value as Prefs['motion']));

  const scale = h('input', { type: 'range', id: 'pref-ui-scale', min: '0.85', max: '1.3', step: '0.05', value: String(ui.uiScale) });
  const scaleValue = h('output', { for: 'pref-ui-scale', text: `${Math.round(ui.uiScale * 100)}%` });
  scale.addEventListener('input', () => {
    scaleValue.textContent = `${Math.round(Number(scale.value) * 100)}%`;
    useUiStore.getState().setUiScale(Number(scale.value));
  });

  const reset = h('button', { class: 'tiny', type: 'button' }, 'Reset accessibility settings');
  reset.addEventListener('click', () => {
    usePrefsStore.getState().resetAccessibility();
    useUiStore.getState().setUiScale(1);
    rerender();
  });

  return h(
    'section',
    { class: 'pref-section' },
    h('h2', { class: 'pref-title', text: 'Accessibility' }),
    h('p', { class: 'pref-lede', text: 'Make the app easier to see, read and use. Each change applies at once and is remembered on this computer. Every control can also be reached with the keyboard; shortcuts can be changed under Keyboard.' }),
    h('h3', { class: 'pref-subtitle', text: 'Seeing' }),
    h('div', { class: 'pref-form' },
      h('label', { for: 'pref-ui-scale', text: 'Interface size' }), h('span', { class: 'pref-range' }, scale, scaleValue),
    ),
    toggle('pref-contrast', 'Higher contrast', 'Brighter text, darker backgrounds and visible borders. With “Match the app”, the editor switches to its high-contrast theme too.', 'highContrast'),
    toggle('pref-colourblind', 'Colour-blind-friendly severity colours', 'Critical, high, medium, low and info stay distinct with red–green colour blindness. Severities are always written out as well.', 'colourBlindSeverity'),
    toggle('pref-focus', 'Always show a strong focus ring', 'A thick outline around whatever has focus, also after a click, so you never lose your place.', 'strongFocus'),
    toggle('pref-links', 'Underline links', 'Links and link-like buttons are underlined, not only coloured.', 'underlineLinks'),
    h('h3', { class: 'pref-subtitle', text: 'Reading' }),
    toggle('pref-spacing', 'Wider spacing in text', 'More room between letters, words and lines in chat answers, findings and descriptions. Some readers with dyslexia find this easier.', 'readableSpacing'),
    h('p', { class: 'pref-hint', text: `The code’s font size and line spacing are under Appearance › Editor (now ${prefs.editorFontSize}px).` }),
    h('h3', { class: 'pref-subtitle', text: 'Motion' }),
    h('div', { class: 'pref-form' }, h('label', { for: 'pref-motion', text: 'Animations' }), motion),
    h('p', { class: 'pref-hint', text: 'Reducing motion stops sliding panels, the typewriter reveal of AI edits and the graph’s settling animation.' }),
    toggle('pref-blink', 'Blinking cursor', 'Turn off for a steady caret in the editor and the terminal.', 'cursorBlink'),
    h('div', { class: 'pref-actions' }, reset),
    h('p', { class: 'pref-hint muted', text: `Defaults: interface 100%, editor ${DEFAULT_PREFS.editorFontSize}px, motion follows the system.` }),
  );
}
