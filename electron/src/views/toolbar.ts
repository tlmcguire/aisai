import type { AppController } from '../app';
import type { ProviderInfo } from '../api/types';
import { h } from '../lib/dom';
import { useAuditStore } from '../state/audit';
import { useUiStore } from '../state/ui';
import { createPackagePicker } from './packagePicker';

/** Toggle a button into its busy state (spinner + disabled). */
export function setBusy(button: HTMLButtonElement, busy: boolean, label?: string): void {
  button.disabled = busy;
  button.classList.toggle('btn-busy', busy);
  const existing = button.querySelector('.spinner');
  if (busy && !existing) {
    button.prepend(h('span', { class: 'spinner', 'aria-hidden': 'true' }));
  } else if (!busy && existing) {
    existing.remove();
  }
  if (label) {
    const text = button.querySelector('.btn-label');
    if (text) text.textContent = label;
  }
}

function button(label: string, className = ''): HTMLButtonElement {
  return h('button', { class: className, type: 'button' }, h('span', { class: 'btn-label', text: label }));
}

export interface Toolbar {
  element: HTMLElement;
  refreshProviders: () => void;
  dispose: () => void;
}

export function createToolbar(controller: AppController, container: HTMLElement): Toolbar {
  const picker = createPackagePicker(controller);

  const scanButton = button('Scan', 'primary');
  const folderButton = button('Open folder…');
  folderButton.title = 'Inspect a project folder on this computer (Ctrl/Cmd+O, or drop a folder on the window)';
  const llmButton = button('Scan LLM');

  const providerChip = h('span', { class: 'badge neutral', text: 'checking provider…' });
  const mockBadge = h('span', { class: 'badge mock', text: 'MOCK DATA', hidden: true });
  const settingsButton = h(
    'button',
    { class: 'ghost icon', type: 'button', title: 'Settings', 'aria-label': 'Settings' },
    '⚙',
  );

  picker.onScan((packageName, version) => {
    void controller.startAudit(packageName, version);
  });

  scanButton.addEventListener('click', () => {
    const pkg = picker.packageName();
    if (!pkg) {
      picker.focus();
      return;
    }
    void controller.startAudit(pkg, picker.version());
  });

  llmButton.addEventListener('click', () => {
    void controller.runScan('llm');
  });

  folderButton.addEventListener('click', () => {
    void controller.chooseFolder();
  });

  // Ctrl/Cmd+O opens the folder dialog from anywhere in the window.
  const onKeyDown = (event: KeyboardEvent): void => {
    if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === 'o') {
      event.preventDefault();
      void controller.chooseFolder();
    }
  };

  // Dropping a folder on the window audits it.
  const hasFiles = (event: DragEvent): boolean => Boolean(event.dataTransfer?.types.includes('Files'));
  const onDragOver = (event: DragEvent): void => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    document.body.classList.add('drop-target');
  };
  const onDragLeave = (event: DragEvent): void => {
    // relatedTarget is null when the pointer leaves the window.
    if (!event.relatedTarget) document.body.classList.remove('drop-target');
  };
  const onDrop = (event: DragEvent): void => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    document.body.classList.remove('drop-target');
    const file = event.dataTransfer?.files[0];
    if (!file) return;
    void window.aisai
      .folderFromDrop(file)
      .then((folder) => {
        if (folder) {
          void controller.startFolderAudit(folder);
        } else {
          useAuditStore.getState().setError('Drop a project folder, not a file.');
        }
      })
      .catch((err: unknown) => {
        useAuditStore.getState().setError(err instanceof Error ? err.message : String(err));
      });
  };
  document.addEventListener('keydown', onKeyDown);
  document.addEventListener('dragover', onDragOver);
  document.addEventListener('dragleave', onDragLeave);
  document.addEventListener('drop', onDrop);

  settingsButton.addEventListener('click', () => {
    useUiStore.getState().setSettingsOpen(true);
  });

  container.append(
    h('div', { class: 'brand', text: 'AISAI' }),
    picker.element,
    scanButton,
    folderButton,
    llmButton,
    h('div', { class: 'toolbar-spacer' }),
    mockBadge,
    providerChip,
    settingsButton,
  );

  const refreshProviders = async (): Promise<void> => {
    const providers = await controller.fetchProviders();
    providerChip.replaceChildren(...providerChips(providers));
  };

  const unsubscribeAudit = useAuditStore.subscribe((state) => {
    setBusy(scanButton, state.busy === 'audit');
    folderButton.disabled = state.busy === 'audit';
    setBusy(llmButton, state.busy?.startsWith('scan:') ?? false);
  });

  const unsubscribeBackend = window.aisai.onBackendStatus((status) => {
    mockBadge.hidden = status.mode !== 'mock';
    // The first refresh happens while the sidecar is still starting, so
    // it sees no providers at all. When the backend becomes ready the
    // list is fetched again, otherwise the chip would keep saying that
    // no provider is configured for the rest of the session.
    if (status.state === 'ready') {
      void refreshProviders();
    }
  });

  return {
    element: container,
    refreshProviders,
    dispose: () => {
      unsubscribeAudit();
      unsubscribeBackend();
      document.removeEventListener('keydown', onKeyDown);
      document.removeEventListener('dragover', onDragOver);
      document.removeEventListener('dragleave', onDragLeave);
      document.removeEventListener('drop', onDrop);
      document.body.classList.remove('drop-target');
      picker.dispose();
    },
  };
}

function providerChips(providers: ProviderInfo[]): Node[] {
  if (!providers.length) {
    return [h('span', { class: 'badge neutral', text: 'no providers configured' })];
  }
  return providers.map((provider) =>
    h(
      'span',
      {
        class: `badge ${provider.ok ? 'ok' : 'bad'}`,
        title: provider.error ?? `${provider.id} (${provider.kind})`,
      },
      provider.ok ? '●' : '○',
      ` ${provider.id} ${provider.ok ? 'up' : 'down'}`,
    ),
  );
}
