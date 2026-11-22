/**
 * Settings sections for the editor's look and for accessibility. Every
 * control writes to state/prefs.ts, which applies it at once.
 */

import { h } from '../lib/dom';
import { t } from '../lib/i18n';
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
    h('option', { value: MATCH_APP, text: t('settings.editorThemeMatchApp') }),
    h('optgroup', { label: t('settings.themeGroupDark') }, ...EDITOR_THEMES.filter((t) => t.dark).map((t) => h('option', { value: t.id, text: t.label }))),
    h('optgroup', { label: t('settings.themeGroupLight') }, ...EDITOR_THEMES.filter((t) => !t.dark).map((t) => h('option', { value: t.id, text: t.label }))),
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
  const sizeValue = h('output', { for: 'pref-editor-size', text: t('settings.pixels', { value: prefs.editorFontSize }) });
  size.addEventListener('input', () => {
    sizeValue.textContent = t('settings.pixels', { value: size.value });
    usePrefsStore.getState().setPref('editorFontSize', Number(size.value));
  });

  const spacing = h('select', { id: 'pref-editor-spacing' },
    h('option', { value: 'compact', text: t('settings.lineSpacingCompact') }),
    h('option', { value: 'normal', text: t('settings.lineSpacingNormal') }),
    h('option', { value: 'relaxed', text: t('settings.lineSpacingRelaxed') }),
  );
  spacing.value = prefs.editorLineSpacing;
  spacing.addEventListener('change', () => usePrefsStore.getState().setPref('editorLineSpacing', spacing.value as Prefs['editorLineSpacing']));

  return h('div', { class: 'pref-group' },
    h('h3', { class: 'pref-subtitle', text: t('settings.editorGroup') }),
    h('div', { class: 'pref-form' },
      h('label', { for: 'pref-editor-theme', text: t('settings.editorThemeLabel') }), themeSelect,
      h('label', { for: 'pref-editor-size', text: t('settings.editorFontSize') }), h('span', { class: 'pref-range' }, size, sizeValue),
      h('label', { for: 'pref-editor-spacing', text: t('settings.editorLineSpacing') }), spacing,
    ),
    preview,
    toggle('pref-minimap', t('settings.showMinimap'), t('settings.showMinimapHint'), 'minimap'),
  );
}

/** The Accessibility section. */
export function renderAccessibility(rerender: () => void): HTMLElement {
  const prefs = usePrefsStore.getState();
  const ui = useUiStore.getState();

  const motion = h('select', { id: 'pref-motion' },
    h('option', { value: 'system', text: t('settings.motionFollowSystem') }),
    h('option', { value: 'reduce', text: t('settings.motionAlwaysReduce') }),
  );
  motion.value = prefs.motion === 'reduce' ? 'reduce' : 'system';
  motion.addEventListener('change', () => usePrefsStore.getState().setPref('motion', motion.value as Prefs['motion']));

  const scale = h('input', { type: 'range', id: 'pref-ui-scale', min: '0.85', max: '1.3', step: '0.05', value: String(ui.uiScale) });
  const scaleValue = h('output', { for: 'pref-ui-scale', text: t('settings.percent', { value: Math.round(ui.uiScale * 100) }) });
  scale.addEventListener('input', () => {
    scaleValue.textContent = t('settings.percent', { value: Math.round(Number(scale.value) * 100) });
    useUiStore.getState().setUiScale(Number(scale.value));
  });

  const reset = h('button', { class: 'tiny', type: 'button' }, t('settings.resetAccessibility'));
  reset.addEventListener('click', () => {
    usePrefsStore.getState().resetAccessibility();
    useUiStore.getState().setUiScale(1);
    rerender();
  });

  return h(
    'section',
    { class: 'pref-section' },
    h('h2', { class: 'pref-title', text: t('settings.accessibilityTitle') }),
    h('p', { class: 'pref-lede', text: t('settings.accessibilityLede') }),
    h('h3', { class: 'pref-subtitle', text: t('settings.seeing') }),
    h('div', { class: 'pref-form' },
      h('label', { for: 'pref-ui-scale', text: t('settings.interfaceSize') }), h('span', { class: 'pref-range' }, scale, scaleValue),
    ),
    toggle('pref-contrast', t('settings.higherContrast'), t('settings.higherContrastHint'), 'highContrast'),
    toggle('pref-colourblind', t('settings.colourBlindSeverity'), t('settings.colourBlindSeverityHint'), 'colourBlindSeverity'),
    toggle('pref-focus', t('settings.strongFocus'), t('settings.strongFocusHint'), 'strongFocus'),
    toggle('pref-links', t('settings.underlineLinks'), t('settings.underlineLinksHint'), 'underlineLinks'),
    h('h3', { class: 'pref-subtitle', text: t('settings.reading') }),
    toggle('pref-spacing', t('settings.readableSpacing'), t('settings.readableSpacingHint'), 'readableSpacing'),
    h('p', { class: 'pref-hint', text: t('settings.editorSizeUnderAppearance', { value: prefs.editorFontSize }) }),
    h('h3', { class: 'pref-subtitle', text: t('settings.motion') }),
    h('div', { class: 'pref-form' }, h('label', { for: 'pref-motion', text: t('settings.animations') }), motion),
    h('p', { class: 'pref-hint', text: t('settings.reduceMotionHint') }),
    toggle('pref-blink', t('settings.blinkingCursor'), t('settings.blinkingCursorHint'), 'cursorBlink'),
    h('div', { class: 'pref-actions' }, reset),
    h('p', { class: 'pref-hint muted', text: t('settings.accessibilityDefaults', { value: DEFAULT_PREFS.editorFontSize }) }),
  );
}
