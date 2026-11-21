import type { AppController } from '../app';
import type { Config, ProviderInfo } from '../api/types';
import { h } from '../lib/dom';
import { useUiStore } from '../state/ui';

/** Settings dialog: providers, effective configuration, provider test. */
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

  const render = (providers: ProviderInfo[], config: Config | null): void => {
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
                `${provider.kind} — ${provider.ok ? 'reachable' : `unavailable: ${provider.error ?? 'unknown error'}`}`,
                provider.models?.length ? ` · models: ${provider.models.join(', ')}` : '',
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
          h('dt', { text: 'chat model' }),
          h('dd', { text: config.chatModel }),
          h('dt', { text: 'scan model' }),
          h('dd', { text: config.scanModel }),
          h('dt', { text: 'embed model' }),
          h('dd', { text: config.embedModel }),
          h('dt', { text: 'context size' }),
          h('dd', { text: String(config.numCtx) }),
        ),
      );
      children.push(
        h('p', {
          class: 'chunks-used',
          text: 'API keys are never sent to the renderer — the backend keeps them.',
        }),
      );
    }

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
              ? `OK — ${info.models?.join(', ') || 'reachable'}`
              : `Failed: ${info.error ?? 'unknown error'}`
            : 'The provider test failed.';
        });
    });

    children.push(
      h('h3', { text: 'Test a provider' }),
      h('div', { class: 'row' }, providerId, providerUrl, providerModel, testButton),
      result,
    );

    body.replaceChildren(...children);
  };

  const refresh = async (): Promise<void> => {
    const [providers, config] = await Promise.all([
      controller.fetchProviders(),
      controller.fetchConfig(),
    ]);
    render(providers, config);
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
