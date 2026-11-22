// @vitest-environment jsdom
/**
 * Tests for searchable settings: finding settings by name, hint, keyword,
 * section, and navigating to them.
 */

import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { AppController } from '../src/app';
import { useLocaleStore } from '../src/lib/i18n';
import { useUiStore } from '../src/state/ui';
import {
  SEARCHABLE_SETTINGS,
  createSettingsDialog,
  searchSettings,
  sectionName,
} from '../src/views/settingsDialog';

function mockController(): AppController {
  return {
    bootstrap: vi.fn(),
    chooseFolder: vi.fn(),
    fetchConfig: vi.fn().mockResolvedValue(null),
    fetchModels: vi.fn().mockResolvedValue(null),
    fetchProviders: vi.fn().mockResolvedValue([]),
    refreshAudits: vi.fn(),
    refreshProviderChip: vi.fn(),
    saveProvider: vi.fn(),
    setModels: vi.fn(),
    setWebSearch: vi.fn().mockResolvedValue(true),
    unloadModels: vi.fn().mockResolvedValue([]),
  } as unknown as AppController;
}

beforeEach(() => {
  document.body.replaceChildren();
  useLocaleStore.getState().setPreference('en');
  useUiStore.getState().setSettingsOpen(false);
  useUiStore.getState().setSettingsSection('server');

  // Stub window.aisai APIs used by settings
  (window as unknown as { aisai: unknown }).aisai = {
    getStatus: () => ({ mode: 'mock', state: 'ready' }),
    onBackendStatus: vi.fn(() => () => undefined),
    onUpdateStatus: vi.fn(() => () => undefined),
    providerKeyStatus: vi.fn().mockResolvedValue({ saved: false, available: true }),
    setLocale: vi.fn(),
  };

  // HTMLDialogElement showModal/close polyfill for jsdom
  if (!HTMLDialogElement.prototype.showModal) {
    HTMLDialogElement.prototype.showModal = function () {
      this.setAttribute('open', '');
    };
  }
  if (!HTMLDialogElement.prototype.close) {
    HTMLDialogElement.prototype.close = function () {
      this.removeAttribute('open');
      this.dispatchEvent(new Event('close'));
    };
  }
});

describe('searchSettings index', () => {
  it('returns empty array for empty or whitespace query', () => {
    expect(searchSettings('')).toEqual([]);
    expect(searchSettings('   ')).toEqual([]);
  });

  it('finds settings by title', () => {
    const themeMatches = searchSettings('theme');
    expect(themeMatches.length).toBeGreaterThan(0);
    expect(themeMatches.some((m) => m.item.section === 'appearance')).toBe(true);

    const langMatches = searchSettings('language');
    expect(langMatches.length).toBeGreaterThan(0);
    expect(langMatches.some((m) => m.item.id === 'pref-language')).toBe(true);
  });

  it('finds settings by keywords', () => {
    const ollamaMatches = searchSettings('ollama');
    expect(ollamaMatches.length).toBeGreaterThan(0);
    expect(ollamaMatches.some((m) => m.item.section === 'server')).toBe(true);

    const geminiMatches = searchSettings('gemini');
    expect(geminiMatches.length).toBeGreaterThan(0);
    expect(geminiMatches.some((m) => m.item.section === 'server')).toBe(true);

    const copilotMatches = searchSettings('copilot');
    expect(copilotMatches.length).toBeGreaterThan(0);
    expect(copilotMatches.some((m) => m.item.id === 'pref-completion')).toBe(true);

    const speedMatches = searchSettings('speed');
    expect(speedMatches.length).toBeGreaterThan(0);
    expect(speedMatches.some((m) => m.item.id === 'pref-language')).toBe(true);
  });

  it('finds settings by description / hint text', () => {
    const cveMatches = searchSettings('cve');
    expect(cveMatches.length).toBeGreaterThan(0);
    expect(cveMatches.some((m) => m.item.id === 'pref-web-search')).toBe(true);

    const dyslexiaMatches = searchSettings('dyslexia');
    expect(dyslexiaMatches.length).toBeGreaterThan(0);
    expect(dyslexiaMatches.some((m) => m.item.id === 'pref-spacing')).toBe(true);
  });

  it('handles multi-word search queries where all words must match', () => {
    const editorThemeMatches = searchSettings('editor theme');
    expect(editorThemeMatches.length).toBeGreaterThan(0);
    for (const match of editorThemeMatches) {
      const fullText = `${match.title} ${match.hint} ${match.sectionLabel} ${match.item.keywords.join(' ')}`.toLowerCase();
      expect(fullText.includes('editor')).toBe(true);
      expect(fullText.includes('theme')).toBe(true);
    }

    const impossible = searchSettings('theme nonexistentterm123');
    expect(impossible).toEqual([]);
  });

  it('is case-insensitive', () => {
    const lower = searchSettings('appearance');
    const upper = searchSettings('APPEARANCE');
    expect(lower.length).toBe(upper.length);
  });

  it('indexes all 9 sections in settings', () => {
    const sectionsCovered = new Set(SEARCHABLE_SETTINGS.map((s) => s.section));
    expect(sectionsCovered.size).toBe(9);
    expect(sectionsCovered.has('server')).toBe(true);
    expect(sectionsCovered.has('models')).toBe(true);
    expect(sectionsCovered.has('sources')).toBe(true);
    expect(sectionsCovered.has('appearance')).toBe(true);
    expect(sectionsCovered.has('accessibility')).toBe(true);
    expect(sectionsCovered.has('keyboard')).toBe(true);
    expect(sectionsCovered.has('layout')).toBe(true);
    expect(sectionsCovered.has('mcp')).toBe(true);
    expect(sectionsCovered.has('about')).toBe(true);
  });

  it('names every section it covers', () => {
    for (const section of new Set(SEARCHABLE_SETTINGS.map((s) => s.section))) {
      expect(sectionName(section)).not.toBe('');
      expect(sectionName(section)).not.toBe(section);
    }
  });
});

describe('settings dialog search UI', () => {
  it('renders search input and responds to user input', () => {
    const controller = mockController();
    const container = document.createElement('div');
    document.body.append(container);
    const dialogHandle = createSettingsDialog(controller, container);

    useUiStore.getState().setSettingsOpen(true);

    const searchInput = container.querySelector('.pref-search-input') as HTMLInputElement;
    expect(searchInput).not.toBeNull();

    // Type a query
    searchInput.value = 'theme';
    searchInput.dispatchEvent(new Event('input'));

    // Check search results button appears in nav
    const searchAllBtn = container.querySelector('.pref-nav-search-all') as HTMLElement;
    expect(searchAllBtn).not.toBeNull();
    expect(searchAllBtn.hidden).toBe(false);

    // Check pane renders search results
    const resultsView = container.querySelector('.pref-search-results');
    expect(resultsView).not.toBeNull();

    const searchItems = container.querySelectorAll('.pref-search-item');
    expect(searchItems.length).toBeGreaterThan(0);

    // Clicking a search item navigates to its section
    const firstItem = searchItems[0] as HTMLButtonElement;
    firstItem.click();

    expect(useUiStore.getState().settingsSection).toBe('appearance');

    // Clear search
    const clearBtn = container.querySelector('.pref-search-clear') as HTMLButtonElement;
    expect(clearBtn.hidden).toBe(false);
    clearBtn.click();

    expect(searchInput.value).toBe('');
    expect(searchAllBtn.hidden).toBe(true);

    dialogHandle.dispose();
  });

  it('supports Cmd+F keyboard shortcut to focus search input', () => {
    const controller = mockController();
    const container = document.createElement('div');
    document.body.append(container);
    const dialogHandle = createSettingsDialog(controller, container);

    useUiStore.getState().setSettingsOpen(true);

    const dialog = container.querySelector('dialog.prefs') as HTMLDialogElement;
    const searchInput = container.querySelector('.pref-search-input') as HTMLInputElement;

    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'f', metaKey: true, bubbles: true }));
    expect(document.activeElement).toBe(searchInput);

    dialogHandle.dispose();
  });

  it('clears search on Escape before closing dialog', () => {
    const controller = mockController();
    const container = document.createElement('div');
    document.body.append(container);
    const dialogHandle = createSettingsDialog(controller, container);

    useUiStore.getState().setSettingsOpen(true);

    const searchInput = container.querySelector('.pref-search-input') as HTMLInputElement;
    searchInput.value = 'contrast';
    searchInput.dispatchEvent(new Event('input'));

    searchInput.focus();
    const escEvent = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    searchInput.dispatchEvent(escEvent);

    expect(searchInput.value).toBe('');
    expect(useUiStore.getState().settingsOpen).toBe(true);

    dialogHandle.dispose();
  });

  it('toggles is-translating class on the settings dialog during language switch', () => {
    const controller = mockController();
    const container = document.createElement('div');
    document.body.append(container);
    const dialogHandle = createSettingsDialog(controller, container);

    useUiStore.getState().setSettingsOpen(true);
    const dialog = container.querySelector('dialog.prefs') as HTMLDialogElement;

    expect(dialog.classList.contains('is-translating')).toBe(false);

    useLocaleStore.setState({ translating: true });
    expect(dialog.classList.contains('is-translating')).toBe(true);

    useLocaleStore.setState({ translating: false });
    expect(dialog.classList.contains('is-translating')).toBe(false);

    dialogHandle.dispose();
  });
});
