import type { AppController } from '../app';
import type { Audit, Config, ModelInfo, ModelState, ProviderInfo, ProviderKind } from '../api/types';
import { formatBytes } from '../lib/format';
import { useAuditStore } from '../state/audit';
import { h } from '../lib/dom';
import { DEFAULT_LAYOUT, LAYOUT_PRESETS, PANE_IDS, PANE_LABELS, useUiStore, type SettingsSection } from '../state/ui';

/** The keep-alive windows the picker offers. */
const KEEP_ALIVE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: '30s', label: '30 seconds' },
  { value: '1m', label: '1 minute' },
  { value: '3m', label: '3 minutes (default)' },
  { value: '5m', label: '5 minutes' },
  { value: '10m', label: '10 minutes' },
  { value: '30m', label: '30 minutes' },
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
    select.append(h('option', { value: current }, current + ' · not installed'));
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
  note: string;
  hosted: boolean;
  model?: string;
}> = [
  { kind: 'ollama', label: 'Ollama', url: 'http://localhost:11434', hosted: false, note: 'Pulls and runs models on this machine. The default.' },
  { kind: 'vllm', label: 'vLLM', url: 'http://localhost:8000/v1', hosted: false, note: 'OpenAI-compatible GPU server. Start it with `vllm serve <model>`. A key is only needed if you set --api-key.' },
  { kind: 'llamacpp', label: 'llama.cpp', url: 'http://localhost:8080/v1', hosted: false, note: 'The `llama-server` binary, serving one GGUF model.' },
  { kind: 'lmstudio', label: 'LM Studio', url: 'http://localhost:1234/v1', hosted: false, note: 'Turn on the local server in LM Studio’s Developer tab.' },
  { kind: 'openai', label: 'OpenAI', url: 'https://api.openai.com/v1', hosted: true, model: 'gpt-5-mini', note: 'ChatGPT models with your OpenAI API key. Code you scan is sent to OpenAI.' },
  { kind: 'anthropic', label: 'Anthropic', url: 'https://api.anthropic.com/v1', hosted: true, model: 'claude-sonnet-5-5', note: 'Claude models with your Anthropic API key. Anthropic has no embedding API, so code search falls back to keywords. Code you scan is sent to Anthropic.' },
  { kind: 'gemini', label: 'Gemini', url: 'https://generativelanguage.googleapis.com/v1beta/openai', hosted: true, model: 'gemini-2.5-flash', note: 'Google Gemini through its OpenAI-compatible endpoint. Code you scan is sent to Google.' },
  { kind: 'openrouter', label: 'OpenRouter', url: 'https://openrouter.ai/api/v1', hosted: true, model: 'anthropic/claude-sonnet-5-5', note: 'One key for hundreds of models. Code you scan is sent to OpenRouter and the model’s provider.' },
  { kind: 'groq', label: 'Groq', url: 'https://api.groq.com/openai/v1', hosted: true, model: 'llama-3.3-70b-versatile', note: 'Fast hosted open models. Code you scan is sent to Groq.' },
  { kind: 'mistral', label: 'Mistral', url: 'https://api.mistral.ai/v1', hosted: true, model: 'codestral-latest', note: 'Mistral and Codestral models. Code you scan is sent to Mistral.' },
];

/** The display name of a provider kind, for the toolbar and the status. */
export function providerLabel(kind: string): string {
  return SERVER_KINDS.find((entry) => entry.kind === kind)?.label ?? kind;
}

const SECTIONS: Array<{ id: SettingsSection; label: string }> = [
  { id: 'server', label: 'Model server' },
  { id: 'models', label: 'Models' },
  { id: 'sources', label: 'Downloaded sources' },
  { id: 'layout', label: 'Layout' },
  { id: 'mcp', label: 'MCP' },
];

/** Turn a transport error into something a person can act on. */
function friendlyError(error: string, url?: string): string {
  const where = url ? ` at ${url}` : '';
  if (/deadline exceeded|timeout/i.test(error)) return `No answer${where}. Is the server running?`;
  if (/connection refused/i.test(error)) return `Nothing is listening${where}. Start the server, or pick another type below.`;
  if (/401|unauthori[sz]ed|invalid.*key/i.test(error)) return 'The service rejected the API key.';
  return error;
}

function relativeTime(iso: string): string {
  const then = Date.parse(iso);
  if (!Number.isFinite(then)) return '';
  const minutes = Math.round((Date.now() - then) / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
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
  const nav = h('nav', { class: 'pref-nav', 'aria-label': 'Settings sections' });
  const pane = h('div', { class: 'pref-pane' });
  const close = h(
    'button',
    { class: 'ghost icon pref-close', type: 'button', 'aria-label': 'Close settings', title: 'Close (Esc)' },
    closeIcon(),
  );

  const dialog = h(
    'dialog',
    { class: 'prefs', 'aria-label': 'Settings' },
    h('div', { class: 'pref-shell' }, nav, h('div', { class: 'pref-main' }, close, pane)),
  );

  close.addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => useUiStore.getState().setSettingsOpen(false));
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

  const navButtons = SECTIONS.map((section) =>
    h(
      'button',
      {
        class: 'pref-nav-item',
        type: 'button',
        'data-section': section.id,
        onclick: () => useUiStore.getState().setSettingsSection(section.id),
      },
      section.label,
    ),
  );
  nav.append(h('div', { class: 'pref-nav-title', text: 'Settings' }), ...navButtons);

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
            ? `${providerLabel(active.kind)} ${active.ok ? 'is connected' : 'is not reachable'}`
            : loading.providers
              ? 'Checking the model server…'
              : 'No model server configured',
        }),
        h('span', {
          text: active
            ? active.ok
              ? `${active.url ?? ''} · ${active.models?.length ?? 0} model(s)`
              : friendlyError(active.error ?? '', active.url)
            : '',
        }),
      ),
    );

    const kindButtons = (hosted: boolean): HTMLElement => h(
      'div',
      { class: 'segmented', role: 'radiogroup', 'aria-label': hosted ? 'Hosted services' : 'Local servers' },
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
      h('div', { class: 'kind-group' }, h('span', { class: 'kind-group-label', text: 'On this machine' }), kindButtons(false)),
      h('div', { class: 'kind-group' }, h('span', { class: 'kind-group-label', text: 'Hosted, bring your own key' }), kindButtons(true)),
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
      placeholder: kind === 'ollama' ? 'qwen3.5:4b' : 'the model the server loaded',
      spellcheck: 'false',
    });
    model.addEventListener('input', () => (draftModel = model.value.trim()));
    const key = h('input', {
      type: 'password',
      id: 'server-key',
      value: draftKey,
      placeholder: keyState?.saved ? 'Saved in the keychain. Type to replace it.' : preset.hosted ? 'Paste your API key' : 'Optional',
      autocomplete: 'off',
      spellcheck: 'false',
    });
    key.addEventListener('input', () => (draftKey = key.value));
    const forget = keyState?.saved
      ? h('button', { class: 'tiny ghost', type: 'button' }, 'Forget key')
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
        ? 'This system has no usable keychain, so the key is kept only until the app quits.'
        : 'Keys are stored encrypted in the system keychain and never written to the config file.',
    });

    const status = h('p', { class: `server-status ${serverTone}`, role: 'status', text: serverStatus });
    const testButton = h('button', { type: 'button' }, 'Test connection');
    const useButton = h('button', { class: 'primary', type: 'button' }, 'Use this server');

    const request = () => ({ kind, url: draftUrl, model: draftModel || undefined, apiKey: draftKey || undefined });

    testButton.addEventListener('click', () => {
      if (!draftUrl) return;
      serverStatus = 'Connecting…';
      serverTone = '';
      status.textContent = serverStatus;
      status.className = 'server-status';
      testButton.disabled = true;
      void controller.testProvider(request()).then((info) => {
        testButton.disabled = false;
        testedModels = info?.models ?? [];
        serverTone = info?.ok ? 'ok' : 'bad';
        serverStatus = info?.ok
          ? `Reachable. ${testedModels.length ? `Models: ${testedModels.slice(0, 6).join(', ')}${testedModels.length > 6 ? '…' : ''}` : 'No models listed.'}`
          : friendlyError(info?.error ?? 'no answer', draftUrl);
        if (info?.ok && !draftModel && testedModels[0]) draftModel = testedModels[0];
        renderSection();
      });
    });

    useButton.addEventListener('click', () => {
      if (!draftUrl) return;
      if (preset.hosted && !draftKey && !keyState?.saved) {
        serverStatus = `Paste your ${preset.label} API key first.`;
        serverTone = 'bad';
        renderSection();
        (pane.querySelector('#server-key') as HTMLElement | null)?.focus();
        return;
      }
      serverStatus = 'Switching…';
      serverTone = '';
      status.textContent = serverStatus;
      useButton.disabled = true;
      void controller.setProvider(request()).then((info) => {
        useButton.disabled = false;
        if (info) lastProviders = [info];
        serverTone = info?.ok ? 'ok' : 'bad';
        serverStatus = info?.ok
          ? `Now using ${preset.label}${draftModel ? ` with ${draftModel}` : ''}. Scans and chat use it from the next request.`
          : `Saved, but the server did not answer: ${info?.error ?? 'unknown error'}`;
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
      h('h2', { class: 'pref-title', text: 'Model server' }),
      h('p', { class: 'pref-lede', text: 'Where scans and chat send their prompts. Everything can stay on this machine.' }),
      current,
      h('div', { class: 'pref-group' }, h('div', { class: 'pref-label', text: 'Server type' }), kinds, h('p', { class: 'pref-hint', text: preset.note })),
      h(
        'div',
        { class: 'pref-form' },
        h('label', { for: 'server-url', text: 'Address' }),
        url,
        h('label', { for: 'server-model', text: 'Model' }),
        h('div', {}, model, modelList),
        needsKey ? h('label', { for: 'server-key', text: 'API key' }) : null,
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
    children.push(h('h2', { class: 'pref-title', text: 'Models' }), h('p', { class: 'pref-lede', text: 'The models the active server runs for scanning, chat and the retrieval index.' }));

    if (!state) {
      children.push(
        h('p', { class: 'empty-state', text: 'The backend did not report any models.' }),
      );
      return h('section', { class: 'settings-section' }, ...children);
    }

    const models = state.models ?? [];
    const memory = deviceMemoryGB();
    const chatSelect = modelSelect('model-chat', models, state.chatModel, 'Chat model');
    const scanSelect = modelSelect('model-scan', models, state.scanModel, 'Scan model');
    const embedSelect = modelSelect('model-embed', models, state.embedModel, 'Embedding model');

    const numCtx = h('input', {
      type: 'number',
      id: 'model-num-ctx',
      value: String(state.numCtx),
      min: '512',
      step: '512',
      'aria-label': 'Context window',
    });

    const keepAlive = h('select', { id: 'model-keep-alive', 'aria-label': 'Keep alive' });
    for (const option of KEEP_ALIVE_OPTIONS) {
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
          h('label', { for: 'model-chat', text: 'Chat' }),
          chatSelect,
        ),
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-scan', text: 'Scan' }),
          scanSelect,
        ),
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-embed', text: 'Embed' }),
          embedSelect,
        ),
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-num-ctx', text: 'Context' }),
          numCtx,
        ),
        h(
          'div',
          { class: 'field' },
          h('label', { for: 'model-keep-alive', text: 'Keep alive' }),
          keepAlive,
        ),
      ),
    );

    // A model larger than half of the machine's memory cannot be loaded
    // without the system falling back to swap, so it is worth saying so.
    const selected = models.find((model) => model.name === state.chatModel);
    const notes: string[] = [];
    if (memory) {
      notes.push('This machine reports ' + memory + 'GB of memory.');
    }
    if (selected && selected.size > 0) {
      notes.push(
        selected.name +
          ' needs about ' +
          selected.sizeText +
          ' of memory while it runs.',
      );
      if (memory && selected.size / 1_000_000_000 > memory * 0.5) {
        notes.push(
          'That is more than half of the memory of this machine; a smaller model will run faster.',
        );
      }
    }
    if (state.error) {
      notes.push('The model list could not be read: ' + state.error);
    }
    if (notes.length > 0) {
      children.push(h('p', { class: 'chunks-used', text: notes.join(' ') }));
    }

    const status = h('span', { class: 'chunks-used model-status', text: modelStatus });
    const applyButton = h('button', { class: 'tiny', type: 'button' }, 'Apply');
    const unloadButton = h('button', { class: 'tiny', type: 'button' }, 'Release memory');

    applyButton.addEventListener('click', () => {
      modelStatus = 'Applying…';
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
            modelStatus = 'The models could not be applied.';
            status.textContent = modelStatus;
            return;
          }
          modelStatus = updated.error ? 'Applied, but: ' + updated.error : 'Applied.';
          lastModels = updated;
          // The picker is re-rendered with the new values, so the status
          // is kept in a variable and restored by the next render.
          renderAll();
        });
    });

    unloadButton.addEventListener('click', () => {
      modelStatus = 'Releasing…';
      status.textContent = modelStatus;
      void controller.unloadModels().then((names) => {
        modelStatus = names
          ? 'Released: ' + (names.join(', ') || 'nothing was loaded')
          : 'The models could not be released.';
        status.textContent = modelStatus;
      });
    });

    children.push(h('div', { class: 'row' }, applyButton, unloadButton, status));

    if (models.length > 0) {
      children.push(
        h('h4', { text: 'Installed models' }),
        h(
          'table',
          { class: 'model-table' },
          h(
            'thead',
            {},
            h(
              'tr',
              {},
              h('th', { text: 'Model' }),
              h('th', { text: 'Size' }),
              h('th', { text: 'Parameters' }),
              h('th', { text: 'Quantization' }),
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

  const renderSources = (): HTMLElement => {
    const audits = useAuditStore.getState().audits;
    const currentId = useAuditStore.getState().current?.id;
    const total = audits.reduce((sum, a) => sum + (a.sourcesDeleted ? 0 : a.sandboxBytes || 0), 0);

    const openFolder = h('button', { type: 'button' }, 'Open a local folder…');
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
        deleted ? 'Deleted' : isArmed ? 'Confirm delete' : 'Delete sources',
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
        del.textContent = 'Deleting…';
        void controller.deleteSourcesOf(audit.id).then(() => renderSection());
      });
      const open = h('button', { class: 'tiny', type: 'button', disabled: audit.id === currentId || undefined }, audit.id === currentId ? 'Open now' : 'Open');
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
        h('td', {}, h('span', { class: `badge ${audit.source === 'local' ? 'neutral' : 'ok'}`, text: audit.source === 'local' ? 'folder' : ({ npm: 'npm', cargo: 'crates.io' } as Record<string, string>)[audit.ecosystem ?? audit.source ?? ''] ?? 'PyPI' })),
        h('td', { class: 'num', text: deleted ? '—' : formatBytes(audit.sandboxBytes || 0) }),
        h('td', { class: 'num', text: String(audit.findingCount ?? 0) }),
        h('td', { class: 'muted', text: relativeTime(audit.updatedAt || audit.createdAt) }),
        h('td', { class: 'actions' }, h('div', { class: 'actions-inner' }, open, del)),
      );
    };

    return h(
      'section',
      { class: 'pref-section' },
      h('h2', { class: 'pref-title', text: 'Downloaded sources' }),
      h('p', {
        class: 'pref-lede',
        text: `Each audit keeps the code it downloaded in a sandbox. ${audits.length} ${audits.length === 1 ? 'audit' : 'audits'}, ${formatBytes(total)} on disk. Deleting sources keeps the findings and the report.`,
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
                h('th', { text: 'Project' }), h('th', { text: 'From' }), h('th', { class: 'num', text: 'Size' }),
                h('th', { class: 'num', text: 'Findings' }), h('th', { text: 'Updated' }), h('th', { text: '' }),
              )),
              h('tbody', {}, ...audits.map(row)),
            ),
          )
        : h('p', { class: 'pref-hint', text: 'No audits yet. Scan a package or open a folder and it appears here.' }),
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
      { class: 'preset-grid', role: 'group', 'aria-label': 'Layout presets' },
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
          h('strong', { text: preset.label }),
          h('small', { text: preset.description }),
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
      return h('label', { class: 'row', for: `show-${id}` }, box, `Show ${PANE_LABELS[id]}`);
    });
    const dock = h(
      'select',
      { id: 'chat-dock', 'aria-label': 'Where the chat panel sits' },
      h('option', { value: 'bottom', selected: ui.chatDock === 'bottom' }, 'Chat below'),
      h('option', { value: 'right', selected: ui.chatDock === 'right' }, 'Chat on the right'),
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
    const px = (v: number): string => `${Math.round(v)} px`;
    const reset = h('button', { type: 'button' }, 'Reset to defaults');
    reset.addEventListener('click', () => {
      ui.resetLayout();
      renderSection();
    });
    return h(
      'section',
      { class: 'pref-section' },
      h('h2', { class: 'pref-title', text: 'Layout' }),
      h('p', {
        class: 'pref-lede',
        text:
          'Pick an arrangement, or build your own: drag a pane by its ⋮⋮ grip (or use its ‹ › buttons) to move it, and drag an edge to resize it. Everything is remembered.',
      }),
      presetGrid(),
      h('h3', { class: 'pref-subtitle', text: 'Panes' }),
      paneControls(),
      h(
        'div',
        { class: 'pref-form sliders' },
        ...slider('ui-scale', 'Interface size', ui.uiScale, 0.85, 1.3, 0.05, (v) => `${Math.round(v * 100)}%`, (v) => useUiStore.getState().setUiScale(v)),
        ...slider('graph-width', 'Left column', ui.graphWidth, 180, 720, 10, px, (v) => useUiStore.getState().setGraphWidth(v)),
        ...slider('files-share', 'File tree share', ui.filesShare, 0.15, 0.85, 0.01, (v) => `${Math.round(v * 100)}%`, (v) => useUiStore.getState().setFilesShare(v)),
        ...slider('rail-width', 'Findings rail', ui.railWidth, 240, 720, 10, px, (v) => useUiStore.getState().setRailWidth(v)),
        ...slider('chat-height', 'Chat height (below)', ui.chatHeight, 120, 640, 10, px, (v) => useUiStore.getState().setChatHeight(v)),
        ...slider('chat-width', 'Chat width (right)', ui.chatWidth, 260, 720, 10, px, (v) => useUiStore.getState().setChatWidth(v)),
      ),
      h(
        'div',
        { class: 'pref-actions' },
        reset,
        h('button', { type: 'button', onclick: () => window.dispatchEvent(new Event('aisai:show-shortcuts')) }, 'Keyboard shortcuts…'),
      ),
      h('p', {
        class: 'pref-hint',
        text: `Defaults: left ${DEFAULT_LAYOUT.graphWidth} px, rail ${DEFAULT_LAYOUT.railWidth} px, chat ${DEFAULT_LAYOUT.chatHeight} px. The interface also grows with large displays.`,
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
        where: 'Run in a terminal',
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
    const tools = [
      ['scan_package', 'Audit a package (PyPI, npm, cargo) or a local folder'],
      ['list_audits / select_audit', 'Resume earlier audits'],
      ['list_files / get_source / get_ast', 'Read the code and its syntax tree'],
      ['run_sast / run_llm_scan', 'Static rules in 14 languages, then an LLM review'],
      ['list_findings / get_finding', 'Triage what was found'],
      ['set_finding_state', 'Confirm, suppress or close a finding'],
      ['propose_fix / apply_patch', 'Draft and apply a patch'],
      ['search_code / ask_codebase', 'Search and ask questions with citations'],
      ['get_dependency_graph', 'See what a package pulls in'],
      ['list_rules / list_languages', 'What the scanner covers'],
      ['export_report / delete_sources', 'SARIF or Markdown report; free disk space'],
    ];
    const blocks = snippets.map((snippet) => {
      const copy = h('button', { class: 'tiny', type: 'button' }, 'Copy');
      copy.addEventListener('click', () => {
        void navigator.clipboard.writeText(snippet.text).then(() => {
          copy.textContent = 'Copied';
          window.setTimeout(() => (copy.textContent = 'Copy'), 1400);
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
      h('h2', { class: 'pref-title', text: 'MCP' }),
      h('p', {
        class: 'pref-lede',
        text: 'AISAI ships an MCP server, so an agent like Claude can run audits, triage findings and draft fixes with the same engine as this app. It uses the model server chosen above.',
      }),
      ...blocks,
      h('p', { class: 'pref-hint', text: 'The MCP server shares this app’s workspace, so audits started by an agent show up here and the other way round. Build it with `make build`; it lands in bin/aisai-mcp.' }),
      h('h4', { text: '20 tools' }),
      h('dl', { class: 'kv tool-list' }, ...tools.flatMap(([name, what]) => [h('dt', {}, h('code', { text: name })), h('dd', { text: what })])),
    );
  };

  /* ---------------- shell ---------------- */

  const renderSection = (): void => {
    const section = useUiStore.getState().settingsSection;
    for (const button of navButtons) {
      button.setAttribute('aria-current', String(button.dataset.section === section));
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
      default:
        content = renderServer();
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

  return {
    dispose: () => {
      unsubscribe();
      unsubscribeAudits();
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
