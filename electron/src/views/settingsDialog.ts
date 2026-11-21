import type { AppController } from '../app';
import type { Config, ModelInfo, ModelState, ProviderInfo } from '../api/types';
import { h } from '../lib/dom';
import { useUiStore } from '../state/ui';

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

/** Settings dialog: providers, models, effective configuration, provider test. */
export function createSettingsDialog(
  controller: AppController,
  container: HTMLElement,
): { dispose: () => void } {
  const body = h('div', {});
  const close = h('button', { class: 'ghost', type: 'button' }, 'Close');

  const dialog = h(
    'dialog',
    {},
    h(
      'div',
      { class: 'dialog-header' },
      h('h2', { text: 'Settings' }),
      close,
    ),
    body,
  );

  close.addEventListener('click', () => dialog.close());
  dialog.addEventListener('close', () => useUiStore.getState().setSettingsOpen(false));
  container.append(dialog);

  const renderProviders = (providers: ProviderInfo[], config: Config | null): HTMLElement => {
    const children: Array<Node> = [];

    children.push(
      h('h3', { text: 'LLM providers' }),
      providers.length
        ? h(
            'dl',
            { class: 'kv' },
            ...providers.flatMap((provider) => [
              h('dt', { text: provider.id }),
              h(
                'dd',
                {},
                provider.kind + ' — ' + (provider.ok ? 'reachable' : 'unavailable: ' + (provider.error ?? 'unknown error')),
                provider.models?.length ? ' · models: ' + provider.models.join(', ') : '',
              ),
            ]),
          )
        : h('p', { class: 'empty-state', text: 'No providers are configured.' }),
    );

    if (config) {
      children.push(
        h('h3', { text: 'Effective configuration' }),
        h(
          'dl',
          { class: 'kv' },
          h('dt', { text: 'workspace' }),
          h('dd', { text: config.workspace }),
          h('dt', { text: 'ollama url' }),
          h('dd', { text: config.ollamaUrl }),
          h('dt', { text: 'context size' }),
          h('dd', { text: String(config.numCtx) }),
          h('dt', { text: 'keep alive' }),
          h('dd', { text: config.keepAlive ?? '3m' }),
        ),
      );
    }

    return h('section', { class: 'settings-section' }, ...children);
  };

  /**
   * renders the model picker.
   *
   * The picker lists the models installed on the Ollama server with the
   * size each one takes, because the size is what decides whether a model
   * fits on the machine at all.
   */
  const renderModels = (state: ModelState | null): HTMLElement => {
    const children: Array<Node> = [];
    children.push(h('h3', { text: 'Models' }));

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

  const renderProviderTest = (config: Config | null): HTMLElement => {
    const providerId = h('input', { type: 'text', value: 'ollama', 'aria-label': 'Provider id' });
    const providerUrl = h('input', {
      type: 'text',
      value: config?.ollamaUrl ?? 'http://localhost:11434',
      'aria-label': 'Provider URL',
    });
    const providerModel = h('input', {
      type: 'text',
      value: config?.chatModel ?? '',
      'aria-label': 'Provider model',
    });
    const result = h('span', { class: 'chunks-used', text: '' });
    const testButton = h('button', { class: 'tiny', type: 'button' }, 'Test connection');

    testButton.addEventListener('click', () => {
      result.textContent = 'Testing…';
      void controller
        .testProvider(providerId.value.trim(), providerUrl.value.trim(), providerModel.value.trim())
        .then((info) => {
          result.textContent = info
            ? info.ok
              ? 'OK — ' + (info.models?.join(', ') || 'reachable')
              : 'Failed: ' + (info.error ?? 'unknown error')
            : 'The provider test failed.';
        });
    });

    return h(
      'section',
      { class: 'settings-section' },
      h('h3', { text: 'Test a provider' }),
      h('div', { class: 'row' }, providerId, providerUrl, providerModel, testButton),
      result,
    );
  };

  let lastProviders: ProviderInfo[] = [];
  let lastConfig: Config | null = null;
  let lastModels: ModelState | null = null;
  // modelStatus survives a re-render, so the message of the last action
  // stays on screen after the picker is rebuilt.
  let modelStatus = '';

  const renderAll = (): void => {
    body.replaceChildren(
      renderProviders(lastProviders, lastConfig),
      renderModels(lastModels),
      renderProviderTest(lastConfig),
    );
  };

  const refresh = async (): Promise<void> => {
    const [providers, config, models] = await Promise.all([
      controller.fetchProviders(),
      controller.fetchConfig(),
      controller.fetchModels(),
    ]);
    lastProviders = providers;
    lastConfig = config;
    lastModels = models;
    renderAll();
  };

  const unsubscribe = useUiStore.subscribe((state, previous) => {
    if (state.settingsOpen && !previous.settingsOpen) {
      void refresh();
      if (!dialog.open) dialog.showModal();
    } else if (!state.settingsOpen && previous.settingsOpen && dialog.open) {
      dialog.close();
    }
  });

  return {
    dispose: unsubscribe,
  };
}
