import type { AppController } from '../app';
import type { UpdateStatus } from '../api/bridge';
import type { ProviderInfo } from '../api/types';
import { h } from '../lib/dom';
import { icon } from '../lib/icons';
import { brandMark } from '../lib/logo';
import { useAuditStore } from '../state/audit';
import { useUiStore } from '../state/ui';
import { createPackagePicker } from './packagePicker';
import { providerLabel } from './settingsDialog';

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
  const brand = h('div', { class: 'brand' }, brandMark(), h('span', { class: 'brand-name', text: 'aisai' }));

  const scanButton = button('Scan', 'primary');
  const folderButton = button('Open folder…');
  folderButton.title = 'Inspect a project folder on this computer (Ctrl/Cmd+O, or drop a folder on the window)';
  const llmButton = button('Scan LLM');

  const providerChip = h('span', { class: 'provider-chips' }, h('span', { class: 'badge neutral', text: 'checking provider…' }));
  const mockBadge = h('span', { class: 'badge mock', text: 'MOCK DATA', hidden: true });
  const settingsButton = h(
    'button',
    { class: 'ghost icon', type: 'button', title: 'Settings', 'aria-label': 'Settings' },
    icon('gear'),
  );

  // The self-updater: shown only while there is something to say.
  const updateButton = h('button', { class: 'tiny update-button', type: 'button', hidden: true });
  let updateState: UpdateStatus = { state: 'idle' };
  updateButton.addEventListener('click', () => {
    if (updateState.state === 'ready') void window.aisai.installUpdate();
    else if (updateState.state === 'manual') void window.aisai.openReleasePage();
  });
  const unsubscribeUpdate = window.aisai.onUpdateStatus((status) => {
    updateState = status;
    const label: Partial<Record<UpdateStatus['state'], string>> = {
      downloading: `Downloading update ${status.version ?? ''} ${status.percent ?? 0}%`,
      ready: `Restart to update to ${status.version ?? 'the new version'}`,
      manual: `Update ${status.version ?? ''} available: download`,
    };
    const text = label[status.state];
    updateButton.hidden = !text;
    updateButton.textContent = text ?? '';
    updateButton.disabled = status.state === 'downloading';
    updateButton.classList.toggle('primary', status.state === 'ready');
    updateButton.title = status.message ?? 'AISAI updates itself in the background';
  });

  picker.onScan((packageName, version, ecosystem) => {
    void controller.startAudit(packageName, version, ecosystem);
  });

  scanButton.addEventListener('click', () => {
    const pkg = picker.packageName();
    if (!pkg) {
      picker.promptForName();
      return;
    }
    void controller.startAudit(pkg, picker.version(), picker.ecosystem());
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
    brand,
    picker.element,
    scanButton,
    folderButton,
    llmButton,
    h('div', { class: 'toolbar-spacer' }),
    updateButton,
    mockBadge,
    providerChip,
    settingsButton,
  );

  const refreshProviders = async (): Promise<void> => {
    const providers = await controller.fetchProviders();
    providerChip.replaceChildren(...providerChips(providers));
    void controller.refreshChatModel(providers);
  };

  const unsubscribeAudit = useAuditStore.subscribe((state) => {
    brand.classList.toggle('is-busy', state.busy !== null);
    setBusy(scanButton, state.busy === 'audit');
    folderButton.disabled = state.busy === 'audit';
    setBusy(llmButton, state.busy?.startsWith('scan:') ?? false);
  });

  const unsubscribeUi = useUiStore.subscribe((state, previous) => {
    if (state.providersStamp !== previous.providersStamp) void refreshProviders();
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
      unsubscribeUi();
      unsubscribeBackend();
      unsubscribeUpdate();
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
    return [h('span', { class: 'badge provider-badge is-bad', role: 'button', tabindex: '0', title: 'Choose a model server', onclick: () => {
      useUiStore.getState().setSettingsSection('server');
      useUiStore.getState().setSettingsOpen(true);
    } }, h('span', { class: 'status-dot', 'aria-hidden': 'true' }), 'No model server')];
  }
  return providers.map((provider) =>
    h(
      'span',
      {
        class: `badge provider-badge ${provider.ok ? 'is-ok' : 'is-bad'}`,
        title: (provider.error ?? `${provider.id} (${provider.kind})`) + ' · click to configure',
        role: 'button',
        tabindex: '0',
        onclick: () => {
          useUiStore.getState().setSettingsSection('server');
          useUiStore.getState().setSettingsOpen(true);
        },
      },
      h('span', { class: 'status-dot', 'aria-hidden': 'true' }),
      `${providerLabel(provider.kind)}${provider.ok ? '' : ' offline'}`,
    ),
  );
}
