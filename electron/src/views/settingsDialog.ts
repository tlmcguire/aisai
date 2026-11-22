/**
 * Settings: a modal <dialog> with one section at a time (model server,
 * models, downloaded sources, appearance, accessibility, keyboard, layout,
 * MCP, about). Sections are re-rendered in place when their data arrives,
 * keeping focus and scroll. The Keyboard and Accessibility sections live in
 * views/keyboardSettings.ts and views/accessibilitySettings.ts.
 */

import type { AppController } from '../app';
import type { UpdateStatus } from '../api/bridge';
import type { Audit, Config, ModelInfo, ModelState, ProviderInfo, ProviderKind } from '../api/types';
import { formatBytes } from '../lib/format';
import { t, tn, useLocaleStore, type MessageKey } from '../lib/i18n';
import { icon } from '../lib/icons';
import { useAuditStore } from '../state/audit';
import { useChatStore } from '../state/chat';
import { renderAccessibility, renderEditorAppearance } from './accessibilitySettings';
import { renderKeyboardSettings } from './keyboardSettings';
import { renderLanguageSetting } from './languageSetting';
import { h } from '../lib/dom';
import { isMac } from '../lib/shortcuts';
import { ACCENTS, DEFAULT_LAYOUT, LAYOUT_PRESETS, PANE_IDS, paneName, useUiStore, type SettingsSection } from '../state/ui';

/** The keep-alive windows the picker offers. */
const keepAliveOptions = (): Array<{ value: string; label: string }> => [
  { value: '30s', label: t('settings.keepAlive30Seconds') },
  { value: '1m', label: t('settings.keepAlive1Minute') },
  { value: '3m', label: t('settings.keepAlive3MinutesDefault') },
  { value: '5m', label: t('settings.keepAlive5Minutes') },
  { value: '10m', label: t('settings.keepAlive10Minutes') },
  { value: '30m', label: t('settings.keepAlive30Minutes') },
];

/**
 * deviceMemoryGB reports the memory of the machine, in gigabytes.
 *
 * Chromium rounds it down to a power of two, so it is a coarse guide. It
 * is used only to warn that a model is too large for the machine, never to
 * refuse one.
 */
function deviceMemoryGB(): number | null {
  const nav = globalThis.navigator as Navigator & { deviceMemory?: number };
  const value = nav.deviceMemory;
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
    return null;
  }
  return value;
}

/**
 * modelLabel renders a model with its size, for a dropdown entry.
 *
 * The label has to stay short: the dropdown is narrow, and a truncated
 * entry is harder to read than a plain name. The parameter count is in the
 * table of installed models below the picker.
 */
function modelLabel(model: ModelInfo): string {
  const parts: string[] = [model.name];
  if (model.sizeText) parts.push(model.sizeText);
  return parts.join(' · ');
}

/**
 * builds a select element for the models of the Ollama server.
 *
 * The models that are installed but not listed by the server (a model that
 * was configured by hand, for instance) are kept as an option, so that
 * switching models never silently discards the current one.
 */
function modelSelect(
  id: string,
  models: ModelInfo[],
  current: string,
  ariaLabel: string,
): HTMLSelectElement {
  const select = h('select', { id, 'aria-label': ariaLabel });
  const names = models.map((model) => model.name);
  if (current && !names.includes(current)) {
    select.append(h('option', { value: current }, t('settings.modelNotInstalled', { name: current })));
  }
  for (const model of models) {
    select.append(h('option', { value: model.name }, modelLabel(model)));
  }
  if (current) {
    select.value = current;
  }
  return select;
}

/** The model servers the app can talk to, with their usual address. */
export const SERVER_KINDS: Array<{
  kind: ProviderKind;
  label: string;
  url: string;
  /** The message that describes the service. */
  noteKey: MessageKey;
  hosted: boolean;
  model?: string;
}> = [
  { kind: 'ollama', label: 'Ollama', url: 'http://localhost:11434', hosted: false, noteKey: 'settings.providerOllamaNote' },
  { kind: 'vllm', label: 'vLLM', url: 'http://localhost:8000/v1', hosted: false, noteKey: 'settings.providerVllmNote' },
  { kind: 'llamacpp', label: 'llama.cpp', url: 'http://localhost:8080/v1', hosted: false, noteKey: 'settings.providerLlamacppNote' },
  { kind: 'lmstudio', label: 'LM Studio', url: 'http://localhost:1234/v1', hosted: false, noteKey: 'settings.providerLmStudioNote' },
  { kind: 'openai', label: 'OpenAI', url: 'https://api.openai.com/v1', hosted: true, model: 'gpt-5-mini', noteKey: 'settings.providerOpenaiNote' },
  { kind: 'anthropic', label: 'Anthropic', url: 'https://api.anthropic.com/v1', hosted: true, model: 'claude-sonnet-5-5', noteKey: 'settings.providerAnthropicNote' },
  { kind: 'gemini', label: 'Gemini', url: 'https://generativelanguage.googleapis.com/v1beta/openai', hosted: true, model: 'gemini-2.5-flash', noteKey: 'settings.providerGeminiNote' },
  { kind: 'openrouter', label: 'OpenRouter', url: 'https://openrouter.ai/api/v1', hosted: true, model: 'anthropic/claude-sonnet-5-5', noteKey: 'settings.providerOpenrouterNote' },
  { kind: 'groq', label: 'Groq', url: 'https://api.groq.com/openai/v1', hosted: true, model: 'llama-3.3-70b-versatile', noteKey: 'settings.providerGroqNote' },
  { kind: 'mistral', label: 'Mistral', url: 'https://api.mistral.ai/v1', hosted: true, model: 'codestral-latest', noteKey: 'settings.providerMistralNote' },
];

/** The display name of a provider kind, for the toolbar and the status. */
export function providerLabel(kind: string): string {
  return SERVER_KINDS.find((entry) => entry.kind === kind)?.label ?? kind;
}

export const SECTIONS: Array<{ id: SettingsSection; label: () => string }> = [
  { id: 'server', label: () => t('settings.navSectionServer') },
  { id: 'models', label: () => t('settings.navSectionModels') },
  { id: 'sources', label: () => t('settings.navSectionSources') },
  { id: 'appearance', label: () => t('settings.navSectionAppearance') },
  { id: 'accessibility', label: () => t('settings.navSectionAccessibility') },
  { id: 'keyboard', label: () => t('settings.navSectionKeyboard') },
  { id: 'layout', label: () => t('settings.navSectionLayout') },
  { id: 'mcp', label: () => t('settings.navSectionMcp') },
  { id: 'about', label: () => t('settings.navSectionAbout') },
];

/** The display label of a settings section. */
export function sectionName(section: SettingsSection): string {
  const found = SECTIONS.find((entry) => entry.id === section);
  return found ? found.label() : section;
}

export interface SettingSearchItem {
  id?: string;
  section: SettingsSection;
  titleKey: MessageKey;
  hintKey?: MessageKey;
  keywords: string[];
}

export const SEARCHABLE_SETTINGS: readonly SettingSearchItem[] = [
  // Server
  {
    id: 'server-kinds',
    section: 'server',
    titleKey: 'settings.serverType',
    hintKey: 'settings.serverLede',
    keywords: ['ollama', 'vllm', 'llamacpp', 'llama.cpp', 'lm studio', 'openai', 'anthropic', 'gemini', 'openrouter', 'groq', 'mistral', 'claude', 'gpt', 'provider', 'backend', 'hosted', 'local'],
  },
  {
    id: 'server-url',
    section: 'server',
    titleKey: 'settings.serverAddress',
    keywords: ['url', 'endpoint', 'address', 'host', 'port', 'http', 'localhost'],
  },
  {
    id: 'server-key',
    section: 'server',
    titleKey: 'settings.serverApiKey',
    hintKey: 'settings.keyPlaceholderSaved',
    keywords: ['api key', 'token', 'secret', 'password', 'keychain', 'auth', 'bearer'],
  },
  {
    id: 'server-model',
    section: 'server',
    titleKey: 'settings.serverModel',
    keywords: ['chat model', 'default model', 'llm', 'model name'],
  },
  {
    id: 'model-keep-alive',
    section: 'server',
    titleKey: 'settings.modelKeepAliveLabel',
    hintKey: 'settings.modelKeepAliveAria',
    keywords: ['keep alive', 'timeout', 'unload', 'idle', 'memory', 'persist', 'keepalive'],
  },

  // Models
  {
    id: 'models-list',
    section: 'models',
    titleKey: 'settings.modelsTitle',
    hintKey: 'settings.modelsLede',
    keywords: ['models', 'chat model', 'scan model', 'embed model', 'context', 'parameters', 'quantization', 'weights', 'size', 'disk', 'switch model'],
  },
  {
    id: 'models-table',
    section: 'models',
    titleKey: 'settings.modelsInstalled',
    hintKey: 'settings.modelsLede',
    keywords: ['installed models', 'table', 'parameters', 'quantization', 'size'],
  },

  // Sources
  {
    id: 'sources-list',
    section: 'sources',
    titleKey: 'settings.sourcesTitle',
    hintKey: 'settings.sourcesEmpty',
    keywords: ['sources', 'audited', 'downloaded', 'cache', 'sandbox', 'delete sources', 'clean cache', 'packages', 'dependencies'],
  },
  {
    id: 'sources-open-folder',
    section: 'sources',
    titleKey: 'settings.sourcesOpenFolder',
    hintKey: 'settings.sourcesTitle',
    keywords: ['open folder', 'local folder', 'directory', 'audit local'],
  },

  // Appearance
  {
    id: 'pref-theme',
    section: 'appearance',
    titleKey: 'settings.themeLabel',
    hintKey: 'settings.appearanceLede',
    keywords: ['theme', 'dark', 'light', 'system', 'mode', 'color scheme', 'verdigris'],
  },
  {
    id: 'pref-accent',
    section: 'appearance',
    titleKey: 'settings.accentColour',
    hintKey: 'settings.appearanceLede',
    keywords: ['accent', 'color', 'teal', 'lime', 'coral', 'violet', 'amber', 'sky', 'swatch', 'tint', 'ground'],
  },
  {
    id: 'pref-language',
    section: 'appearance',
    titleKey: 'settings.language',
    hintKey: 'settings.languageSystem',
    keywords: ['language', 'locale', 'translation', 'speed', 'english', 'deutsch', 'german', 'español', 'spanish', 'français', 'french', 'japanese', 'nihongo', 'sprache', 'idioma', 'langue', '言語'],
  },
  {
    id: 'pref-editor-theme',
    section: 'appearance',
    titleKey: 'settings.editorThemeLabel',
    hintKey: 'settings.editorThemeMatchApp',
    keywords: ['editor theme', 'syntax highlighting', 'code colors', 'dark', 'light'],
  },
  {
    id: 'pref-editor-size',
    section: 'appearance',
    titleKey: 'settings.editorFontSize',
    hintKey: 'settings.editorSizeUnderAppearance',
    keywords: ['font size', 'text size', 'code size', 'editor size', 'pixels', 'px'],
  },
  {
    id: 'pref-editor-spacing',
    section: 'appearance',
    titleKey: 'settings.editorLineSpacing',
    keywords: ['line spacing', 'line height', 'compact', 'normal', 'relaxed', 'leading'],
  },
  {
    id: 'pref-minimap',
    section: 'appearance',
    titleKey: 'settings.showMinimap',
    hintKey: 'settings.showMinimapHint',
    keywords: ['minimap', 'code map', 'overview', 'outline', 'scroll preview'],
  },
  {
    id: 'pref-completion',
    section: 'appearance',
    titleKey: 'settings.completionLabel',
    hintKey: 'settings.completionHint',
    keywords: ['completion', 'inline completion', 'autocomplete', 'ghost text', 'copilot', 'ai suggestion'],
  },
  {
    id: 'pref-web-search',
    section: 'appearance',
    titleKey: 'settings.webSearchLabel',
    hintKey: 'settings.webSearchHint',
    keywords: ['web search', 'internet', 'browse', 'cve', 'advisories', 'docs', 'online'],
  },
  {
    id: 'pref-accept-fixed',
    section: 'appearance',
    titleKey: 'settings.acceptFixLabel',
    hintKey: 'settings.acceptFixHint',
    keywords: ['accept fix', 'mark fixed', 'findings', 'resolved', 'auto triage', 'patches'],
  },

  // Accessibility
  {
    id: 'pref-ui-scale',
    section: 'accessibility',
    titleKey: 'settings.interfaceSize',
    hintKey: 'settings.layoutInterfaceSize',
    keywords: ['interface size', 'ui scale', 'zoom', 'magnification', 'text size', 'percent'],
  },
  {
    id: 'pref-contrast',
    section: 'accessibility',
    titleKey: 'settings.higherContrast',
    hintKey: 'settings.higherContrastHint',
    keywords: ['contrast', 'high contrast', 'wcag', 'visibility', 'borders'],
  },
  {
    id: 'pref-colourblind',
    section: 'accessibility',
    titleKey: 'settings.colourBlindSeverity',
    hintKey: 'settings.colourBlindSeverityHint',
    keywords: ['colour blind', 'colorblind', 'deuteranopia', 'protanopia', 'severity colors', 'daltonism'],
  },
  {
    id: 'pref-focus',
    section: 'accessibility',
    titleKey: 'settings.strongFocus',
    hintKey: 'settings.strongFocusHint',
    keywords: ['focus ring', 'strong focus', 'keyboard focus', 'outline', 'focus indicator'],
  },
  {
    id: 'pref-links',
    section: 'accessibility',
    titleKey: 'settings.underlineLinks',
    hintKey: 'settings.underlineLinksHint',
    keywords: ['underline links', 'links', 'hyperlinks', 'accessible links'],
  },
  {
    id: 'pref-spacing',
    section: 'accessibility',
    titleKey: 'settings.readableSpacing',
    hintKey: 'settings.readableSpacingHint',
    keywords: ['readable spacing', 'dyslexia', 'line length', 'reading', 'letter spacing'],
  },
  {
    id: 'pref-motion',
    section: 'accessibility',
    titleKey: 'settings.animations',
    hintKey: 'settings.reduceMotionHint',
    keywords: ['animations', 'reduce motion', 'typewriter', 'sliding panels', 'motion', 'transitions'],
  },
  {
    id: 'pref-blink',
    section: 'accessibility',
    titleKey: 'settings.blinkingCursor',
    hintKey: 'settings.blinkingCursorHint',
    keywords: ['blinking cursor', 'steady caret', 'terminal cursor', 'cursor blink'],
  },
  {
    id: 'pref-reset-accessibility',
    section: 'accessibility',
    titleKey: 'settings.resetAccessibility',
    hintKey: 'settings.accessibilityDefaults',
    keywords: ['reset accessibility', 'defaults', 'restore'],
  },

  // Keyboard
  {
    id: 'keys-filter',
    section: 'keyboard',
    titleKey: 'settings.navSectionKeyboard',
    hintKey: 'settings.keysFilterPlaceholder',
    keywords: ['keyboard shortcuts', 'shortcuts', 'keybindings', 'hotkeys', 'remap', 'keys', 'cmd', 'ctrl', 'alt', 'shift'],
  },
  {
    id: 'keys-reset-all',
    section: 'keyboard',
    titleKey: 'settings.keysResetAll',
    keywords: ['reset shortcuts', 'restore keybindings', 'default keys'],
  },

  // Layout
  {
    id: 'layout-presets',
    section: 'layout',
    titleKey: 'settings.layoutTitle',
    hintKey: 'settings.layoutLede',
    keywords: ['layout presets', 'wide code', 'big graph', 'chat focus', 'panes arrangement'],
  },
  {
    id: 'graph-width',
    section: 'layout',
    titleKey: 'settings.layoutLeftColumn',
    hintKey: 'settings.layoutDefaults',
    keywords: ['left column', 'graph width', 'dependency tree width', 'sidebar size'],
  },
  {
    id: 'files-share',
    section: 'layout',
    titleKey: 'settings.layoutFileTreeShare',
    hintKey: 'settings.layoutDefaults',
    keywords: ['file tree share', 'files height', 'tree split', 'left split'],
  },
  {
    id: 'rail-width',
    section: 'layout',
    titleKey: 'settings.layoutFindingsRail',
    hintKey: 'settings.layoutDefaults',
    keywords: ['findings rail', 'rail width', 'right column', 'issues panel'],
  },
  {
    id: 'chat-dock',
    section: 'layout',
    titleKey: 'settings.chatDock',
    keywords: ['chat dock', 'chat below', 'chat right', 'chat position', 'docking'],
  },
  {
    id: 'chat-height',
    section: 'layout',
    titleKey: 'settings.layoutChatHeight',
    hintKey: 'settings.layoutDefaults',
    keywords: ['chat height', 'chat size', 'chat panel height'],
  },
  {
    id: 'chat-width',
    section: 'layout',
    titleKey: 'settings.layoutChatWidth',
    hintKey: 'settings.layoutDefaults',
    keywords: ['chat width', 'chat panel width'],
  },
  {
    id: 'layout-reset-btn',
    section: 'layout',
    titleKey: 'settings.resetToDefaults',
    hintKey: 'settings.layoutDefaults',
    keywords: ['reset layout', 'default layout', 'reset panes', 'restore window size'],
  },

  // MCP
  {
    id: 'mcp-claude-code',
    section: 'mcp',
    titleKey: 'settings.navSectionMcp',
    hintKey: 'settings.mcpLede',
    keywords: ['claude code', 'mcp add', 'mcp servers', 'model context protocol', 'terminal'],
  },
  {
    id: 'mcp-claude-desktop',
    section: 'mcp',
    titleKey: 'settings.navSectionMcp',
    hintKey: 'settings.mcpLede',
    keywords: ['claude desktop', 'claude_desktop_config.json', 'desktop app'],
  },
  {
    id: 'mcp-cursor',
    section: 'mcp',
    titleKey: 'settings.navSectionMcp',
    hintKey: 'settings.mcpLede',
    keywords: ['cursor', '.cursor/mcp.json', 'ide mcp'],
  },
  {
    id: 'mcp-tools-list',
    section: 'mcp',
    titleKey: 'settings.mcpToolCount',
    hintKey: 'settings.mcpHint',
    keywords: ['mcp tools', 'list_rules', 'list_languages', 'audit_package', 'read_file', 'grep_code'],
  },

  // About
  {
    id: 'about-version-info',
    section: 'about',
    titleKey: 'settings.aboutTitle',
    hintKey: 'settings.aboutAppVersion',
    keywords: ['version', 'build', 'commit', 'about aisai', 'license', 'platform'],
  },
  {
    id: 'about-update-check',
    section: 'about',
    titleKey: 'settings.updateCheck',
    hintKey: 'settings.updateUpToDate',
    keywords: ['check for updates', 'download update', 'upgrade', 'new version'],
  },
];

export interface SearchMatch {
  item: SettingSearchItem;
  title: string;
  hint: string;
  sectionLabel: string;
}

/** Finds settings matching any words in the query. */
export function searchSettings(query: string, items: readonly SettingSearchItem[] = SEARCHABLE_SETTINGS): SearchMatch[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const words = q.split(/\s+/).filter(Boolean);
  const results: SearchMatch[] = [];

  for (const item of items) {
    const title = t(item.titleKey);
    const hint = item.hintKey ? t(item.hintKey) : '';
    const secLabel = sectionName(item.section);
    const corpus = `${title} ${hint} ${secLabel} ${item.keywords.join(' ')}`.toLowerCase();

    if (words.every((w) => corpus.includes(w))) {
      results.push({ item, title, hint, sectionLabel: secLabel });
    }
  }

  return results;
}

/** Turn a transport error into something a person can act on. */
function friendlyError(error: string, url?: string): string {
  if (/deadline exceeded|timeout/i.test(error)) {
    return url ? t('settings.errorNoAnswerAt', { url }) : t('settings.errorNoAnswer');
  }
  if (/connection refused/i.test(error)) {
    return url ? t('settings.errorNothingListeningAt', { url }) : t('settings.errorNothingListening');
  }
  if (/401|unauthori[sz]ed|invalid.*key/i.test(error)) return t('settings.errorKeyRejected');
  return error;
}

function relativeTime(iso: string): string {
  const then = Date.parse(iso);
  if (!Number.isFinite(then)) return '';
  const minutes = Math.round((Date.now() - then) / 60000);
  if (minutes < 1) return t('settings.justNow');
  if (minutes < 60) return tn('settings.minutesAgo', minutes);
  const hours = Math.round(minutes / 60);
  if (hours < 24) return tn('settings.hoursAgo', hours);
  return new Date(then).toLocaleDateString();
}

/**
 * Settings: a preferences window with a sidebar of sections.
 *
 * It opens instantly. Each section renders from what was loaded last time
 * and refreshes in place when its own request returns, so a slow or absent
 * model server never holds the whole window.
 */
export function createSettingsDialog(
  controller: AppController,
  container: HTMLElement,
): { dispose: () => void } {
  const nav = h('nav', { class: 'pref-nav', 'aria-label': t('settings.navSections') });
  const pane = h('div', { class: 'pref-pane' });
  const close = h(
    'button',
    { class: 'ghost icon pref-close', type: 'button', 'aria-label': t('settings.navClose'), title: t('settings.navCloseTitle') },
    closeIcon(),
  );

  const dialog = h(
    'dialog',
    { class: 'prefs', 'aria-label': t('settings.navTitle') },
    h('div', { class: 'pref-shell' }, nav, h('div', { class: 'pref-main' }, close, pane)),
  );

  close.addEventListener('click', () => dialog.close());
  // The store says whether the window is open, so the dialog follows it.
  const onClose = (): void => useUiStore.getState().setSettingsOpen(false);
  dialog.addEventListener('close', onClose);
  // A click on the backdrop closes the window, like a sheet.
  dialog.addEventListener('mousedown', (event) => {
    if (event.target === dialog) dialog.close();
  });
  container.append(dialog);

  let lastProviders: ProviderInfo[] = [];
  let lastConfig: Config | null = null;
  let lastModels: ModelState | null = null;
  let loading = { providers: false, config: false, models: false };
  let modelStatus = '';

  let searchQuery = '';
  let searchSection: SettingsSection | 'all' = 'all';

  const searchInput = h('input', {
    type: 'search',
    class: 'pref-search-input',
    placeholder: t('settings.searchPlaceholder'),
    'aria-label': t('settings.searchAria'),
  });
  const searchClear = h(
    'button',
    {
      class: 'pref-search-clear',
      type: 'button',
      'aria-label': t('settings.searchClear'),
      hidden: true,
    },
    icon('x'),
  );
  const searchBox = h(
    'div',
    { class: 'pref-search' },
    h('span', { class: 'pref-search-icon' }, icon('search')),
    searchInput,
    searchClear,
  );

  const searchResultsLabel = h('span', { class: 'pref-nav-label', text: t('settings.searchResultsTitle') });
  const searchResultsBadge = h('span', { class: 'pref-nav-badge' });
  const searchResultsBtn = h(
    'button',
    {
      class: 'pref-nav-item pref-nav-search-all',
      type: 'button',
      'data-section': 'search',
      hidden: true,
      onclick: () => {
        searchSection = 'all';
        renderSection();
      },
    },
    searchResultsLabel,
    searchResultsBadge,
  );

  const navButtons = SECTIONS.map((section) => {
    const labelSpan = h('span', { class: 'pref-nav-label', text: section.label() });
    const badgeSpan = h('span', { class: 'pref-nav-badge', hidden: true });
    return h(
      'button',
      {
        class: 'pref-nav-item',
        type: 'button',
        'data-section': section.id,
        onclick: () => {
          if (searchQuery) {
            searchSection = section.id;
          }
          useUiStore.getState().setSettingsSection(section.id);
          renderSection();
        },
      },
      labelSpan,
      badgeSpan,
    );
  });
  nav.append(
    h('div', { class: 'pref-nav-title', text: t('settings.navTitle') }),
    searchBox,
    searchResultsBtn,
    ...navButtons,
  );

  const clearSearch = (): void => {
    searchInput.value = '';
    searchQuery = '';
    searchSection = 'all';
    searchClear.hidden = true;
    searchResultsBtn.hidden = true;
    for (const button of navButtons) {
      button.classList.remove('is-dimmed');
      const badge = button.querySelector('.pref-nav-badge') as HTMLElement | null;
      if (badge) {
        badge.hidden = true;
        badge.textContent = '';
      }
    }
    renderSection();
  };

  const updateSearch = (): void => {
    const q = searchInput.value.trim();
    searchQuery = q;
    searchClear.hidden = q.length === 0;

    if (!q) {
      clearSearch();
      return;
    }

    const matches = searchSettings(q, SEARCHABLE_SETTINGS);
    const countBySection = new Map<SettingsSection, number>();
    for (const match of matches) {
      countBySection.set(match.item.section, (countBySection.get(match.item.section) ?? 0) + 1);
    }

    searchResultsBtn.hidden = false;
    searchResultsBadge.textContent = String(matches.length);

    for (const button of navButtons) {
      const secId = button.dataset.section as SettingsSection;
      const count = countBySection.get(secId) ?? 0;
      const badge = button.querySelector('.pref-nav-badge') as HTMLElement | null;
      if (badge) {
        badge.hidden = count === 0;
        badge.textContent = count > 0 ? String(count) : '';
      }
      if (count === 0) {
        button.classList.add('is-dimmed');
      } else {
        button.classList.remove('is-dimmed');
      }
    }

    renderSection();
  };

  searchInput.addEventListener('input', () => {
    searchSection = 'all';
    updateSearch();
  });
  searchClear.addEventListener('click', () => {
    clearSearch();
    searchInput.focus();
  });

  dialog.addEventListener('keydown', (event) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'f') {
      event.preventDefault();
      searchInput.focus();
      searchInput.select();
      return;
    }
    if (
      event.key === '/' &&
      document.activeElement !== searchInput &&
      !(document.activeElement instanceof HTMLInputElement) &&
      !(document.activeElement instanceof HTMLTextAreaElement)
    ) {
      event.preventDefault();
      searchInput.focus();
      return;
    }
    if (event.key === 'Escape' && document.activeElement === searchInput && searchInput.value.length > 0) {
      event.preventDefault();
      event.stopPropagation();
      clearSearch();
      return;
    }
  });

  /* ---------------- model server ---------------- */

  let draftKind: ProviderKind | null = null;
  let draftUrl = '';
  let draftModel = '';
  let draftKey = '';
  let serverStatus = '';
  let serverTone: 'ok' | 'bad' | '' = '';
  let testedModels: string[] = [];
  let keyState: { kind: ProviderKind; saved: boolean; available: boolean } | null = null;

  const renderServer = (): HTMLElement => {
    const active = lastProviders[0];
    const activeKind = (active?.kind as ProviderKind | undefined) ?? 'ollama';
    const kind = draftKind ?? activeKind;
    const preset = SERVER_KINDS.find((entry) => entry.kind === kind) ?? SERVER_KINDS[0];
    if (!draftUrl) {
      draftUrl =
        kind === activeKind && active?.url
          ? active.url
          : kind === 'ollama' && lastConfig?.ollamaUrl
            ? lastConfig.ollamaUrl
            : preset.url;
    }
    if (!draftModel && kind === activeKind) draftModel = lastConfig?.chatModel ?? '';

    const current = h(
      'div',
      { class: `server-current ${active ? (active.ok ? 'is-ok' : 'is-bad') : 'is-pending'}` },
      h('span', { class: 'status-dot', 'aria-hidden': 'true' }),
      h(
        'div',
        { class: 'server-current-text' },
        h('strong', {
          text: active
            ? active.ok
              ? t('settings.serverConnected', { name: providerLabel(active.kind) })
              : t('settings.serverNotReachable', { name: providerLabel(active.kind) })
            : loading.providers
              ? t('settings.serverChecking')
              : t('settings.serverNotConfigured'),
        }),
        h('span', {
          text: active
            ? active.ok
              ? tn('settings.serverModelCount', active.models?.length ?? 0, { url: active.url ?? '' })
              : friendlyError(active.error ?? '', active.url)
            : '',
        }),
      ),
    );

    const kindButtons = (hosted: boolean): HTMLElement => h(
      'div',
      { class: 'segmented', role: 'radiogroup', 'aria-label': hosted ? t('settings.serverHostedServices') : t('settings.serverLocalServers') },
      ...SERVER_KINDS.filter((entry) => entry.hosted === hosted).map((entry) =>
        h(
          'button',
          {
            type: 'button',
            role: 'radio',
            'aria-checked': String(entry.kind === kind),
            class: 'segment',
            onclick: () => {
              if (draftKind === entry.kind) return;
              draftKind = entry.kind;
              draftUrl = entry.kind === activeKind && active?.url ? active.url : entry.url;
              draftModel = entry.kind === activeKind ? (lastConfig?.chatModel ?? '') : (entry.model ?? '');
              keyState = null;
              testedModels = [];
              serverStatus = '';
              serverTone = '';
              renderSection();
            },
          },
          entry.label,
        ),
      ),
    );
    const kinds = h(
      'div',
      { class: 'kind-groups' },
      h('div', { class: 'kind-group' }, h('span', { class: 'kind-group-label', text: t('settings.serverOnThisMachine') }), kindButtons(false)),
      h('div', { class: 'kind-group' }, h('span', { class: 'kind-group-label', text: t('settings.serverHostedOwnKey') }), kindButtons(true)),
    );

    // Whether a key for this service is already in the keychain.
    if (keyState === null || keyState.kind !== kind) {
      keyState = { kind, saved: false, available: true };
      void window.aisai
        .providerKeyStatus(kind)
        .then((status) => {
          keyState = { kind, ...status };
          if (dialog.open && useUiStore.getState().settingsSection === 'server') renderSection();
        })
        .catch(() => undefined);
    }
    const needsKey = preset.hosted || kind === 'vllm';

    const url = h('input', { type: 'text', id: 'server-url', value: draftUrl, spellcheck: 'false' });
    url.addEventListener('input', () => (draftUrl = url.value.trim()));
    const modelList = h('datalist', { id: 'server-models' }, ...testedModels.map((m) => h('option', { value: m })));
    const model = h('input', {
      type: 'text',
      id: 'server-model',
      value: draftModel,
      list: 'server-models',
      placeholder: kind === 'ollama' ? 'qwen3.5:4b' : t('settings.modelPlaceholderLoaded'),
      spellcheck: 'false',
    });
    model.addEventListener('input', () => (draftModel = model.value.trim()));
    const key = h('input', {
      type: 'password',
      id: 'server-key',
      value: draftKey,
      placeholder: keyState?.saved
        ? t('settings.keyPlaceholderSaved')
        : preset.hosted
          ? t('settings.keyPlaceholderPaste')
          : t('settings.keyPlaceholderOptional'),
      autocomplete: 'off',
      spellcheck: 'false',
    });
    key.addEventListener('input', () => (draftKey = key.value));
    const forget = keyState?.saved
      ? h('button', { class: 'tiny ghost', type: 'button' }, t('settings.forgetKey'))
      : null;
    forget?.addEventListener('click', () => {
      void window.aisai.forgetProviderKey(kind).then(() => {
        keyState = { kind, saved: false, available: keyState?.available ?? true };
        renderSection();
      });
    });
    const keyNote = h('p', {
      class: 'pref-hint key-note',
      text: keyState && !keyState.available
        ? t('settings.keyNoteNoKeychain')
        : t('settings.keyNoteEncrypted'),
    });

    const status = h('p', { class: `server-status ${serverTone}`, role: 'status', text: serverStatus });
    const testButton = h('button', { type: 'button' }, t('settings.testConnection'));
    const useButton = h('button', { class: 'primary', type: 'button' }, t('settings.useThisServer'));

    const request = () => ({ kind, url: draftUrl, model: draftModel || undefined, apiKey: draftKey || undefined });

    testButton.addEventListener('click', () => {
      if (!draftUrl) return;
      serverStatus = t('settings.connecting');
      serverTone = '';
      status.textContent = serverStatus;
      status.className = 'server-status';
      testButton.disabled = true;
      void controller.testProvider(request()).then((info) => {
        testButton.disabled = false;
        testedModels = info?.models ?? [];
        serverTone = info?.ok ? 'ok' : 'bad';
        serverStatus = info?.ok
          ? testedModels.length
            ? t('settings.reachableWithModels', {
                models: `${testedModels.slice(0, 6).join(', ')}${testedModels.length > 6 ? '…' : ''}`,
              })
            : t('settings.reachableNoModels')
          : friendlyError(info?.error ?? t('settings.noAnswer'), draftUrl);
        if (info?.ok && !draftModel && testedModels[0]) draftModel = testedModels[0];
        renderSection();
      });
    });

    useButton.addEventListener('click', () => {
      if (!draftUrl) return;
      if (preset.hosted && !draftKey && !keyState?.saved) {
        serverStatus = t('settings.pasteApiKeyFirst', { name: preset.label });
        serverTone = 'bad';
        renderSection();
        (pane.querySelector('#server-key') as HTMLElement | null)?.focus();
        return;
      }
      serverStatus = t('settings.switching');
      serverTone = '';
      status.textContent = serverStatus;
      useButton.disabled = true;
      void controller.setProvider(request()).then((info) => {
        useButton.disabled = false;
        if (info) lastProviders = [info];
        serverTone = info?.ok ? 'ok' : 'bad';
        serverStatus = info?.ok
          ? draftModel
            ? t('settings.nowUsingModel', { name: preset.label, model: draftModel })
            : t('settings.nowUsing', { name: preset.label })
          : t('settings.savedButNoAnswer', { error: info?.error ?? t('settings.unknownError') });
        draftKind = null;
        draftKey = '';
        void loadModels();
        renderSection();
        void controller.refreshProviderChip();
      });
    });

    return h(
      'section',
      { class: 'pref-section' },
      h('h2', { class: 'pref-title', text: t('settings.serverTitle') }),
      h('p', { class: 'pref-lede', text: t('settings.serverLede') }),
      current,
      h('div', { class: 'pref-group' }, h('div', { class: 'pref-label', text: t('settings.serverType') }), kinds, h('p', { class: 'pref-hint', text: t(preset.noteKey) })),
      h(
        'div',
        { class: 'pref-form' },
        h('label', { for: 'server-url', text: t('settings.serverAddress') }),
        url,
        h('label', { for: 'server-model', text: t('settings.serverModel') }),
        h('div', {}, model, modelList),
        needsKey ? h('label', { for: 'server-key', text: t('settings.serverApiKey') }) : null,
        needsKey ? h('div', { class: 'key-field' }, key, forget) : null,
      ),
      needsKey ? keyNote : null,
      h('div', { class: 'pref-actions' }, testButton, useButton),
      status,
    );
  };

  /* ---------------- models ---------------- */

  const renderModels = (state: ModelState | null): HTMLElement => {
    const children: Array<Node> = [];
    children.push(
      h('h2', { class: 'pref-title', text: t('settings.modelsTitle') }),
      h('p', { class: 'pref-lede', text: t('settings.modelsLede') }),
    );

    if (!state) {
      children.push(
        h('p', { class: 'empty-state', text: t('settings.modelsEmpty') }),
      );
      return h('section', { class: 'settings-section' }, ...children);
    }

    const models = state.models ?? [];
    const memory = deviceMemoryGB();
    const chatSelect = modelSelect('model-chat', models, state.chatModel, t('settings.modelChatAria'));
    const scanSelect = modelSelect('model-scan', models, state.scanModel, t('settings.modelScanAria'));
    const embedSelect = modelSelect('model-embed', models, state.embedModel, t('settings.modelEmbedAria'));

    const numCtx = h('input', {
      type: 'number',
      id: 'model-num-ctx',
      value: String(state.numCtx),
      min: '512',
      step: '512',
      'aria-label': t('settings.modelContextAria'),
    });

    const keepAlive = h('select', { id: 'model-keep-alive', 'aria-label': t('settings.modelKeepAliveAria') });
    for (const option of keepAliveOptions()) {
      keepAlive.append(h('option', { value: option.value }, option.label));
    }
    if (state.keepAlive) {
      keepAlive.value = state.keepAlive;
    }

    children.push(
      h(
        'div',
        { class: 'model-picker' },
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-chat', text: t('settings.modelChatLabel') }),
          chatSelect,
        ),
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-scan', text: t('settings.modelScanLabel') }),
          scanSelect,
        ),
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-embed', text: t('settings.modelEmbedLabel') }),
          embedSelect,
        ),
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-num-ctx', text: t('settings.modelContextLabel') }),
          numCtx,
        ),
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-keep-alive', text: t('settings.modelKeepAliveLabel') }),
          keepAlive,
        ),
      ),
    );

    // A model larger than half of the machine's memory cannot be loaded
    // without the system falling back to swap, so it is worth saying so.
    const selected = models.find((model) => model.name === state.chatModel);
    const notes: string[] = [];
    if (memory) {
      notes.push(t('settings.machineMemory', { gb: memory }));
    }
    if (selected && selected.size > 0) {
      notes.push(t('settings.modelNeedsMemory', { name: selected.name, size: selected.sizeText }));
      if (memory && selected.size / 1_000_000_000 > memory * 0.5) {
        notes.push(t('settings.modelMemoryTooBig'));
      }
    }
    if (state.error) {
      notes.push(t('settings.modelListError', { error: state.error }));
    }
    if (notes.length > 0) {
      children.push(h('p', { class: 'chunks-used', text: notes.join(' ') }));
    }

    const status = h('span', { class: 'chunks-used model-status', text: modelStatus });
    const applyButton = h('button', { class: 'tiny', type: 'button' }, t('settings.modelsApply'));
    const unloadButton = h('button', { class: 'tiny', type: 'button' }, t('settings.modelsReleaseMemory'));

    applyButton.addEventListener('click', () => {
      modelStatus = t('settings.modelsApplying');
      status.textContent = modelStatus;
      void controller
        .setModels({
          chatModel: chatSelect.value,
          scanModel: scanSelect.value,
          embedModel: embedSelect.value,
          numCtx: Number(numCtx.value) || state.numCtx,
          keepAlive: keepAlive.value,
        })
        .then((updated) => {
          if (!updated) {
            modelStatus = t('settings.modelsNotApplied');
            status.textContent = modelStatus;
            return;
          }
          modelStatus = updated.error ? t('settings.modelsAppliedBut', { error: updated.error }) : t('settings.modelsApplied');
          lastModels = updated;
          // The picker is re-rendered with the new values, so the status
          // is kept in a variable and restored by the next render.
          renderAll();
        });
    });

    unloadButton.addEventListener('click', () => {
      modelStatus = t('settings.modelsReleasing');
      status.textContent = modelStatus;
      void controller.unloadModels().then((names) => {
        modelStatus = names
          ? names.length
            ? t('settings.modelsReleased', { names: names.join(', ') })
            : t('settings.modelsReleasedNothing')
          : t('settings.modelsNotReleased');
        status.textContent = modelStatus;
      });
    });

    children.push(h('div', { class: 'row' }, applyButton, unloadButton, status));

    if (models.length > 0) {
      children.push(
        h('h4', { text: t('settings.modelsInstalled') }),
        h(
          'table',
          { class: 'model-table' },
          h(
            'thead',
            {},
            h(
              'tr',
              {},
              h('th', { text: t('settings.columnModel') }),
              h('th', { text: t('settings.columnSize') }),
              h('th', { text: t('settings.columnParameters') }),
              h('th', { text: t('settings.columnQuantization') }),
            ),
          ),
          h(
            'tbody',
            {},
            ...models.map((model) =>
              h(
                'tr',
                {},
                h('td', { text: model.name }),
                h('td', { text: model.sizeText }),
                h('td', { text: model.parameterSize ?? '—' }),
                h('td', { text: model.quantizationLevel ?? '—' }),
              ),
            ),
          ),
        ),
      );
    }

    return h('section', { class: 'settings-section' }, ...children);
  };


  /* ---------------- downloaded sources ---------------- */

  const armed = new Set<string>();
  const removedSources = new Set<string>();

  const renderSources = (): HTMLElement => {
    // A source that was deleted is no longer a source: drop it at once,
    // without waiting for the audit list to come back from the backend.
    const audits = useAuditStore.getState().audits.filter((a) => !a.sourcesDeleted && !removedSources.has(a.id));
    const currentId = useAuditStore.getState().current?.id;
    const total = audits.reduce((sum, a) => sum + (a.sourcesDeleted ? 0 : a.sandboxBytes || 0), 0);

    const openFolder = h('button', { type: 'button' }, t('settings.sourcesOpenFolder'));
    openFolder.addEventListener('click', () => {
      dialog.close();
      void controller.chooseFolder();
    });

    const row = (audit: Audit): HTMLElement => {
      const deleted = audit.sourcesDeleted;
      const isArmed = armed.has(audit.id);
      const del = h(
        'button',
        { class: `tiny${isArmed ? ' is-armed' : ''}`, type: 'button', disabled: deleted || undefined },
        deleted ? t('settings.sourcesDeleted') : isArmed ? t('settings.sourcesConfirmDelete') : t('settings.sourcesDelete'),
      );
      del.addEventListener('click', () => {
        if (!armed.has(audit.id)) {
          armed.add(audit.id);
          renderSection();
          window.setTimeout(() => {
            if (armed.delete(audit.id)) renderSection();
          }, 5000);
          return;
        }
        armed.delete(audit.id);
        del.disabled = true;
        del.textContent = t('settings.sourcesDeleting');
        void controller.deleteSourcesOf(audit.id).then((result) => {
          if (result) removedSources.add(audit.id);
          renderSection();
        });
      });
      const open = h(
        'button',
        { class: 'tiny', type: 'button', disabled: audit.id === currentId || undefined },
        audit.id === currentId ? t('settings.sourcesOpenNow') : t('settings.sourcesOpen'),
      );
      open.addEventListener('click', () => {
        void controller.selectAudit(audit.id);
        dialog.close();
      });
      return h(
        'tr',
        { class: audit.id === currentId ? 'is-current' : '' },
        h(
          'td',
          {},
          h('span', { class: 'source-name', text: audit.package }),
          audit.version ? h('span', { class: 'source-version', text: audit.version }) : null,
        ),
        h('td', {}, h('span', { class: `badge ${audit.source === 'local' ? 'neutral' : 'ok'}`, text: audit.source === 'local' ? t('settings.sourceFolder') : ({ npm: 'npm', cargo: 'crates.io' } as Record<string, string>)[audit.ecosystem ?? audit.source ?? ''] ?? 'PyPI' })),
        h('td', { class: 'num', text: deleted ? '—' : formatBytes(audit.sandboxBytes || 0) }),
        h('td', { class: 'num', text: String(audit.findingCount ?? 0) }),
        h('td', { class: 'muted', text: relativeTime(audit.updatedAt || audit.createdAt) }),
        h('td', { class: 'actions' }, h('div', { class: 'actions-inner' }, open, del)),
      );
    };

    return h(
      'section',
      { class: 'pref-section' },
      h('h2', { class: 'pref-title', text: t('settings.sourcesTitle') }),
      h('p', {
        class: 'pref-lede',
        text: tn('settings.auditsOnDisk', audits.length, { size: formatBytes(total) }),
      }),
      h('div', { class: 'pref-actions' }, openFolder),
      audits.length
        ? h(
            'div',
            { class: 'table-wrap' },
            h(
              'table',
              { class: 'model-table sources-table' },
              h('thead', {}, h('tr', {},
                h('th', { text: t('settings.columnProject') }), h('th', { text: t('settings.columnFrom') }),
                h('th', { class: 'num', text: t('settings.columnSize') }),
                h('th', { class: 'num', text: t('settings.columnFindings') }),
                h('th', { text: t('settings.columnUpdated') }), h('th', { text: '' }),
              )),
              h('tbody', {}, ...audits.map(row)),
            ),
          )
        : h('p', { class: 'pref-hint', text: t('settings.sourcesEmpty') }),
    );
  };


  /* ---------------- appearance and behaviour ---------------- */

  const renderAppearance = (): HTMLElement => {
    const ui = useUiStore.getState();
    const themeSelect = h(
      'select',
      { id: 'pref-theme', 'aria-label': t('settings.themeLabel') },
      h('option', { value: 'dark' }, t('settings.themeDarkVerdigris')),
      h('option', { value: 'light' }, t('settings.themeLight')),
      h('option', { value: 'system' }, t('settings.themeMatchSystem')),
    );
    themeSelect.value = ui.theme;
    themeSelect.addEventListener('change', () => useUiStore.getState().setTheme(themeSelect.value as 'dark' | 'light' | 'system'));
    const swatches = h(
      'div',
      { class: 'swatch-row', role: 'group', 'aria-label': t('settings.accentColour') },
      ...ACCENTS.map((accent) =>
        h('button', {
          type: 'button',
          class: 'swatch',
          title: accent.label(),
          'aria-label': accent.label(),
          'aria-pressed': String(ui.accent === accent.id),
          style: `background:${accent.dark[0]}`,
          onclick: () => {
            useUiStore.getState().setAccent(accent.id);
            renderSection();
          },
        }),
      ),
    );
    const check = (id: string, label: string, hint: string, value: boolean, apply: (v: boolean) => void): HTMLElement => {
      const box = h('input', { type: 'checkbox', id });
      box.checked = value;
      box.addEventListener('change', () => apply(box.checked));
      return h('div', { class: 'pref-check' }, h('label', { for: id }, box, ' ', label), h('p', { class: 'pref-hint', text: hint }));
    };
    return h(
      'section',
      { class: 'pref-section' },
      h('h2', { class: 'pref-title', text: t('settings.appearanceTitle') }),
      h('p', { class: 'pref-lede', text: t('settings.appearanceLede') }),
      h('div', { class: 'pref-form' },
        h('label', { for: 'pref-theme', text: t('settings.themeLabel') }), themeSelect,
        h('label', { text: t('settings.accentColour') }), swatches,
        ...renderLanguageSetting(),
      ),
      renderEditorAppearance(),
      h('h3', { class: 'pref-subtitle', text: t('settings.behaviour') }),
      check('pref-completion', t('settings.completionLabel'), t('settings.completionHint', { keys: isMac() ? '⌘⌥K' : 'Ctrl+Alt+K' }), ui.completionEnabled, (v) => useUiStore.getState().setCompletionEnabled(v)),
      check('pref-web-search', t('settings.webSearchLabel'), t('settings.webSearchHint'), Boolean(lastConfig?.webSearch), (v) => {
        void controller.setWebSearch(v).then((enabled) => {
          if (lastConfig) lastConfig = { ...lastConfig, webSearch: enabled };
        });
      }),
      ...webSearchWarning(),
      h('p', { class: 'pref-hint', text: t('settings.commandsHint') }),
      check('pref-accept-fixed', t('settings.acceptFixLabel'), t('settings.acceptFixHint'), ui.acceptMarksFixed, (v) => useUiStore.getState().setAcceptMarksFixed(v)),
    );
  };

  /** Web search needs a model that can call tools; say so when this one cannot. */
  const webSearchWarning = (): HTMLElement[] => {
    const model = useChatStore.getState().model;
    if (!model.available || model.tools !== 'no') return [];
    return [
      h('p', { class: 'pref-warning', role: 'note' },
        h('strong', { text: t('settings.modelNoTools', { name: model.name }) }),
        t('settings.modelNoToolsRest'),
      ),
    ];
  };

  /* ---------------- about ---------------- */

  let updateState: UpdateStatus = { state: 'idle' };
  const unsubscribeUpdate = window.aisai.onUpdateStatus((status) => {
    updateState = status;
    if (dialog.open && useUiStore.getState().settingsSection === 'about') renderSection();
  });

  const renderAbout = (): HTMLElement => {
    const backend = window.aisai.getStatus();
    const busy = updateState.state === 'checking' || updateState.state === 'downloading';
    const ready = updateState.state === 'ready';
    const manual = updateState.state === 'manual';
    const button = h(
      'button',
      { type: 'button', class: ready || manual ? 'primary' : '', disabled: busy || undefined },
      busy
        ? updateState.state === 'checking'
          ? t('settings.updateChecking')
          : t('settings.updateDownloading', { percent: updateState.percent ?? 0 })
        : ready
          ? t('settings.updateRestart')
          : manual
            ? t('settings.updateDownload')
            : t('settings.updateCheck'),
    );
    button.addEventListener('click', () => {
      if (ready) void window.aisai.installUpdate();
      else if (manual) void window.aisai.openReleasePage();
      else void window.aisai.checkForUpdates();
    });
    const note =
      updateState.state === 'idle'
        ? t('settings.updateUpToDate')
        : updateState.state === 'available'
          ? t('settings.updateVersionAvailable', { version: updateState.version ?? '' })
          : updateState.message || (updateState.version ? t('settings.updateVersion', { version: updateState.version }) : '');
    return h(
      'section',
      { class: 'pref-section' },
      h('h2', { class: 'pref-title', text: t('settings.aboutTitle') }),
      h('p', { class: 'pref-lede', text: t('settings.aboutLede') }),
      h(
        'div',
        { class: 'pref-form' },
        h('label', { text: t('settings.aboutAppVersion') }),
        h('div', { text: __APP_VERSION__ }),
        h('label', { text: t('settings.aboutBackend') }),
        h('div', { text: backend.version ? `${backend.version} (${backend.mode})` : backend.mode }),
      ),
      h('div', { class: 'pref-actions' }, button),
      h('p', { class: 'pref-hint', text: note }),
    );
  };

  /* ---------------- layout ---------------- */

  const slider = (
    id: string,
    label: string,
    value: number,
    min: number,
    max: number,
    step: number,
    format: (v: number) => string,
    apply: (v: number) => void,
  ): Node[] => {
    const out = h('output', { for: id, text: format(value) });
    const input = h('input', { type: 'range', id, min: String(min), max: String(max), step: String(step), value: String(value) });
    input.addEventListener('input', () => {
      const v = Number(input.value);
      out.textContent = format(v);
      apply(v);
    });
    return [h('label', { for: id, text: label }), input, out];
  };

  const presetGrid = (): HTMLElement => {
    const ui = useUiStore.getState();
    return h(
      'div',
      { class: 'preset-grid', role: 'group', 'aria-label': t('settings.layoutPresets') },
      ...LAYOUT_PRESETS.map((preset) => {
        const cols = preset.order
          .filter((id) => id === 'main' || !preset.hidden.includes(id))
          .map((id) => h('i', { class: id === 'main' ? 'main' : '' }));
        const card = h(
          'button',
          { class: 'preset-card', type: 'button', 'aria-pressed': String(ui.layoutPreset === preset.id) },
          h(
            'span',
            { class: `preset-thumb dock-${preset.chatDock}`, 'aria-hidden': 'true' },
            h('span', { class: 'cols' }, ...cols),
            h('i', { class: 'chat' }),
          ),
          h('strong', { text: preset.label() }),
          h('small', { text: preset.description() }),
        );
        card.addEventListener('click', () => {
          useUiStore.getState().applyPreset(preset.id);
          renderSection();
        });
        return card;
      }),
    );
  };

  const paneControls = (): HTMLElement => {
    const ui = useUiStore.getState();
    const checks = PANE_IDS.filter((id) => id !== 'main').map((id) => {
      const box = h('input', { type: 'checkbox', id: `show-${id}`, checked: !ui.hiddenPanes.includes(id) });
      box.checked = !ui.hiddenPanes.includes(id);
      box.addEventListener('change', () => {
        useUiStore.getState().setPaneHidden(id, !box.checked);
        renderSection();
      });
      return h('label', { class: 'row', for: `show-${id}` }, box, t('settings.showPane', { name: paneName(id) }));
    });
    const dock = h(
      'select',
      { id: 'chat-dock', 'aria-label': t('settings.chatDock') },
      h('option', { value: 'bottom', selected: ui.chatDock === 'bottom' }, t('settings.chatDockBottom')),
      h('option', { value: 'right', selected: ui.chatDock === 'right' }, t('settings.chatDockRight')),
    );
    dock.value = ui.chatDock;
    dock.addEventListener('change', () => {
      useUiStore.getState().setChatDock(dock.value === 'right' ? 'right' : 'bottom');
      renderSection();
    });
    return h('div', { class: 'pref-checks' }, ...checks, dock);
  };

  const renderLayout = (): HTMLElement => {
    const ui = useUiStore.getState();
    const px = (v: number): string => t('settings.pixelsSpaced', { value: Math.round(v) });
    const reset = h('button', { type: 'button' }, t('settings.resetToDefaults'));
    reset.addEventListener('click', () => {
      ui.resetLayout();
      renderSection();
    });
    return h(
      'section',
      { class: 'pref-section' },
      h('h2', { class: 'pref-title', text: t('settings.layoutTitle') }),
      h('p', {
        class: 'pref-lede',
        text: t('settings.layoutLede'),
      }),
      presetGrid(),
      h('h3', { class: 'pref-subtitle', text: t('settings.layoutPanes') }),
      paneControls(),
      h(
        'div',
        { class: 'pref-form sliders' },
        ...slider('ui-scale', t('settings.layoutInterfaceSize'), ui.uiScale, 0.85, 1.3, 0.05, (v) => t('settings.percent', { value: Math.round(v * 100) }), (v) => useUiStore.getState().setUiScale(v)),
        ...slider('graph-width', t('settings.layoutLeftColumn'), ui.graphWidth, 180, 720, 10, px, (v) => useUiStore.getState().setGraphWidth(v)),
        ...slider('files-share', t('settings.layoutFileTreeShare'), ui.filesShare, 0.15, 0.85, 0.01, (v) => t('settings.percent', { value: Math.round(v * 100) }), (v) => useUiStore.getState().setFilesShare(v)),
        ...slider('rail-width', t('settings.layoutFindingsRail'), ui.railWidth, 240, 720, 10, px, (v) => useUiStore.getState().setRailWidth(v)),
        ...slider('chat-height', t('settings.layoutChatHeight'), ui.chatHeight, 120, 640, 10, px, (v) => useUiStore.getState().setChatHeight(v)),
        ...slider('chat-width', t('settings.layoutChatWidth'), ui.chatWidth, 260, 720, 10, px, (v) => useUiStore.getState().setChatWidth(v)),
      ),
      h(
        'div',
        { class: 'pref-actions' },
        reset,
        h('button', { type: 'button', onclick: () => window.dispatchEvent(new Event('aisai:show-shortcuts')) }, t('settings.keyboardShortcuts')),
      ),
      h('p', {
        class: 'pref-hint',
        text: t('settings.layoutDefaults', {
          left: DEFAULT_LAYOUT.graphWidth,
          rail: DEFAULT_LAYOUT.railWidth,
          chat: DEFAULT_LAYOUT.chatHeight,
        }),
      }),
    );
  };

  /* ---------------- MCP ---------------- */

  const renderMcp = (): HTMLElement => {
    const binary = 'aisai-mcp';
    const snippets: Array<{ id: string; label: string; where: string; text: string }> = [
      {
        id: 'claude-code',
        label: 'Claude Code',
        where: t('settings.mcpRunInTerminal'),
        text: `claude mcp add aisai -- ${binary}`,
      },
      {
        id: 'claude-desktop',
        label: 'Claude Desktop',
        where: 'claude_desktop_config.json',
        text: JSON.stringify({ mcpServers: { aisai: { command: binary } } }, null, 2),
      },
      {
        id: 'cursor',
        label: 'Cursor',
        where: '.cursor/mcp.json',
        text: JSON.stringify({ mcpServers: { aisai: { command: binary } } }, null, 2),
      },
      {
        id: 'vscode',
        label: 'VS Code',
        where: '.vscode/mcp.json',
        text: JSON.stringify({ servers: { aisai: { type: 'stdio', command: binary } } }, null, 2),
      },
    ];
    const tools: Array<[string, MessageKey]> = [
      ['scan_package', 'settings.mcpToolAuditPackage'],
      ['list_audits / select_audit', 'settings.mcpToolResumeAudits'],
      ['list_files / get_source / get_ast', 'settings.mcpToolReadCode'],
      ['run_sast / run_llm_scan', 'settings.mcpToolStaticRules'],
      ['list_findings / get_finding', 'settings.mcpToolTriageFindings'],
      ['set_finding_state', 'settings.mcpToolFindingState'],
      ['propose_fix / apply_patch', 'settings.mcpToolProposeFix'],
      ['search_code / ask_codebase', 'settings.mcpToolSearchCode'],
      ['get_dependency_graph', 'settings.mcpToolDependencyGraph'],
      ['list_rules / list_languages', 'settings.mcpToolListRules'],
      ['export_report / delete_sources', 'settings.mcpToolExportReport'],
    ];
    const blocks = snippets.map((snippet) => {
      const copy = h('button', { class: 'tiny', type: 'button' }, t('settings.mcpCopy'));
      copy.addEventListener('click', () => {
        void navigator.clipboard.writeText(snippet.text).then(() => {
          copy.textContent = t('settings.mcpCopied');
          window.setTimeout(() => (copy.textContent = t('settings.mcpCopy')), 1400);
        });
      });
      return h(
        'div',
        { class: 'snippet' },
        h('div', { class: 'snippet-head' }, h('strong', { text: snippet.label }), h('span', { class: 'pref-hint', text: snippet.where }), h('span', { class: 'grow' }), copy),
        h('pre', {}, h('code', { text: snippet.text })),
      );
    });
    return h(
      'section',
      { class: 'pref-section' },
      h('h2', { class: 'pref-title', text: t('settings.mcpTitle') }),
      h('p', {
        class: 'pref-lede',
        text: t('settings.mcpLede'),
      }),
      ...blocks,
      h('p', { class: 'pref-hint', text: t('settings.mcpHint') }),
      h('h4', { text: t('settings.mcpToolCount') }),
      h('dl', { class: 'kv tool-list' }, ...tools.flatMap(([name, what]) => [h('dt', {}, h('code', { text: name })), h('dd', { text: t(what) })])),
    );
  };

  /* ---------------- shell ---------------- */

  const focusAndHighlight = (id: string): void => {
    window.setTimeout(() => {
      const el = pane.querySelector(`#${CSS.escape(id)}`) as HTMLElement | null;
      if (el) {
        el.scrollIntoView({ behavior: 'smooth', block: 'center' });
        const targetToHighlight = el.closest('.pref-check, .pref-form, .pref-group, .kind-groups, .preset-grid, .field') ?? el;
        targetToHighlight.classList.add('is-highlighted');
        window.setTimeout(() => targetToHighlight.classList.remove('is-highlighted'), 2200);
        if (typeof (el as HTMLElement).focus === 'function' && el.tagName !== 'DIV') {
          el.focus();
        }
      }
    }, 60);
  };

  const renderSearchResultsView = (results: SearchMatch[]): HTMLElement => {
    if (results.length === 0) {
      return h(
        'section',
        { class: 'pref-section pref-search-results' },
        h('h2', { class: 'pref-title', text: t('settings.searchResultsTitle') }),
        h('p', { class: 'pref-lede', text: t('settings.searchNoMatches', { query: searchQuery }) }),
      );
    }

    const list = h('div', { class: 'pref-search-list', role: 'list' });
    for (const res of results) {
      const itemBtn = h(
        'button',
        {
          class: 'pref-search-item',
          type: 'button',
          role: 'listitem',
          onclick: () => {
            searchSection = res.item.section;
            useUiStore.getState().setSettingsSection(res.item.section);
            renderSection();
            if (res.item.id) {
              focusAndHighlight(res.item.id);
            }
          },
        },
        h(
          'div',
          { class: 'pref-search-item-header' },
          h('span', { class: 'pref-search-section-badge', text: res.sectionLabel }),
          h('span', { class: 'pref-search-item-title', text: res.title }),
        ),
        res.hint ? h('p', { class: 'pref-search-item-hint', text: res.hint }) : null,
        h('span', { class: 'pref-search-item-action', text: t('settings.searchJumpHint', { section: res.sectionLabel }) }),
      );
      list.append(itemBtn);
    }

    return h(
      'section',
      { class: 'pref-section pref-search-results' },
      h('h2', { class: 'pref-title', text: t('settings.searchResultsTitle') }),
      h('p', { class: 'pref-lede', text: tn('settings.searchResultsCount', results.length) }),
      list,
    );
  };

  const renderSection = (): void => {
    const section = useUiStore.getState().settingsSection;
    const inSearchAll = searchQuery.length > 0 && searchSection === 'all';

    searchResultsBtn.setAttribute('aria-current', String(inSearchAll));
    for (const button of navButtons) {
      button.setAttribute('aria-current', String(!inSearchAll && button.dataset.section === section));
    }

    if (inSearchAll) {
      pane.dataset.section = 'search';
      const results = searchSettings(searchQuery, SEARCHABLE_SETTINGS);
      pane.replaceChildren(renderSearchResultsView(results));
      return;
    }

    const scroll = pane.scrollTop;
    const sameSection = pane.dataset.section === section;
    pane.dataset.section = section;
    let content: HTMLElement;
    switch (section) {
      case 'models':
        content = renderModels(lastModels);
        break;
      case 'sources':
        content = renderSources();
        break;
      case 'layout':
        content = renderLayout();
        break;
      case 'mcp':
        content = renderMcp();
        break;
      case 'appearance':
        content = renderAppearance();
        break;
      case 'accessibility':
        content = renderAccessibility(renderSection);
        break;
      case 'keyboard':
        content = renderKeyboardSettings(controller);
        break;
      case 'about':
        content = renderAbout();
        break;
      default:
        content = renderServer();
    }

    if (searchQuery.length > 0) {
      const banner = h(
        'div',
        { class: 'pref-search-banner' },
        h('span', { text: t('settings.searchJumpHint', { section: sectionName(section) }) }),
        h(
          'button',
          {
            type: 'button',
            class: 'pref-search-banner-btn',
            onclick: () => {
              searchSection = 'all';
              renderSection();
            },
          },
          t('settings.searchResultsTitle'),
        ),
      );
      content.prepend(banner);

      const sectionMatches = searchSettings(searchQuery, SEARCHABLE_SETTINGS.filter((item) => item.section === section));
      window.setTimeout(() => {
        for (const match of sectionMatches) {
          if (match.item.id) {
            const el = pane.querySelector(`#${CSS.escape(match.item.id)}`);
            if (el) {
              const target = el.closest('.pref-check, .pref-form, .pref-group, .kind-groups, .preset-grid, .field') ?? el;
              target.classList.add('is-highlighted');
              window.setTimeout(() => target.classList.remove('is-highlighted'), 2200);
            }
          }
        }
      }, 50);
    }

    // Keep focus and scroll when a section refreshes in place.
    const focusedId = (document.activeElement as HTMLElement | null)?.id;
    pane.replaceChildren(content);
    if (sameSection) {
      pane.scrollTop = scroll;
      if (focusedId) (pane.querySelector(`#${CSS.escape(focusedId)}`) as HTMLElement | null)?.focus();
    } else {
      content.classList.add('is-entering');
    }
  };
  const renderAll = renderSection;

  const loadModels = async (): Promise<void> => {
    loading.models = true;
    lastModels = await controller.fetchModels();
    loading.models = false;
    if (dialog.open) renderSection();
  };

  // Each request updates its own section as soon as it lands.
  const refresh = (): void => {
    loading = { providers: true, config: true, models: true };
    void controller.fetchProviders().then((providers) => {
      lastProviders = providers;
      loading.providers = false;
      if (dialog.open) renderSection();
    });
    void controller.fetchConfig().then((config) => {
      lastConfig = config;
      loading.config = false;
      if (dialog.open) renderSection();
    });
    void loadModels();
    void controller.refreshAudits();
  };

  const unsubscribe = useUiStore.subscribe((state, previous) => {
    if (state.settingsOpen && !previous.settingsOpen) {
      clearSearch();
      renderSection();
      if (!dialog.open) dialog.showModal();
      refresh();
    } else if (!state.settingsOpen && previous.settingsOpen && dialog.open) {
      dialog.close();
    }
    if (state.settingsSection !== previous.settingsSection && dialog.open) renderSection();
  });
  const unsubscribeAudits = useAuditStore.subscribe((state, previous) => {
    if (state.audits !== previous.audits && dialog.open && useUiStore.getState().settingsSection === 'sources') {
      renderSection();
    }
  });
  const unsubscribeTranslating = useLocaleStore.subscribe((state) => {
    dialog.classList.toggle('is-translating', state.translating);
  });
  const unsubscribeLocale = useLocaleStore.subscribe((state, previous) => {
    if (state.locale !== previous.locale && dialog.open) {
      searchInput.placeholder = t('settings.searchPlaceholder');
      searchInput.setAttribute('aria-label', t('settings.searchAria'));
      searchClear.setAttribute('aria-label', t('settings.searchClear'));
      if (searchQuery.length > 0) {
        updateSearch();
      }
    }
  });

  // The window may already be open when this is built, and it stays open
  // when the language changes: the interface is not built a second time.
  if (useUiStore.getState().settingsOpen) {
    renderSection();
    dialog.showModal();
    refresh();
  }

  return {
    dispose: () => {
      // The window is going away with the rest of the interface. It must not
      // report a close afterwards: that would undo the change that rebuilt
      // the interface, and close the new window with it.
      dialog.removeEventListener('close', onClose);
      if (dialog.open) dialog.close();
      unsubscribe();
      unsubscribeAudits();
      unsubscribeUpdate();
      unsubscribeLocale();
      unsubscribeTranslating();
    },
  };
}

function closeIcon(): SVGElement {
  const doc = new DOMParser().parseFromString(
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>',
    'image/svg+xml',
  );
  return document.importNode(doc.documentElement, true) as unknown as SVGElement;
}
