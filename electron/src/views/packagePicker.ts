import type { AppController } from '../app';
import { h } from '../lib/dom';
import { useAuditStore } from '../state/audit';

/**
 * Packages that are worth offering before anything has been audited.
 *
 * The package index has no search endpoint, so the suggestions come from
 * two places: the packages that were audited before, and a short list of
 * the packages people actually install.
 */
const POPULAR_PACKAGES = [
  'requests',
  'urllib3',
  'flask',
  'django',
  'numpy',
  'pandas',
  'scipy',
  'boto3',
  'pyyaml',
  'jinja2',
  'click',
  'setuptools',
  'pytest',
  'cryptography',
  'pillow',
  'sqlalchemy',
  'aiohttp',
  'fastapi',
  'pydantic',
  'rich',
];

/** How long to wait before asking the index about the typed name. */
const LOOKUP_DELAY_MS = 250;

/** The largest number of suggestions shown at once. */
const MAX_SUGGESTIONS = 8;

/** A package that can be audited. */
interface Suggestion {
  /** The name of the package. */
  name: string;
  /** The version it was last audited at, when it is a known package. */
  version: string;
  /** Whether the package was audited before. */
  recent: boolean;
}

export interface PackagePicker {
  /** The elements to append to the toolbar. */
  element: DocumentFragment;
  /** The package name to audit. */
  packageName(): string;
  /** The version to audit; an empty string means the latest release. */
  version(): string;
  /** Registers the handler called when the user asks to scan. */
  onScan(handler: (packageName: string, version: string) => void): void;
  /** Focuses the package field. */
  focus(): void;
  /** Releases the listeners and timers. */
  dispose(): void;
}

/**
 * Builds the package picker: a combobox for the package name and a dropdown
 * for the version.
 *
 * The version is never typed by hand. It is either "latest" or one of the
 * versions the index actually published, which removes the most common way
 * of auditing the wrong release.
 */
export function createPackagePicker(controller: AppController): PackagePicker {
  const input = h('input', {
    type: 'text',
    id: 'package-input',
    placeholder: 'flask',
    role: 'combobox',
    'aria-expanded': 'false',
    'aria-autocomplete': 'list',
    'aria-controls': 'package-suggestions',
    'aria-label': 'Package name',
    autocomplete: 'off',
    spellcheck: 'false',
  });

  const list = h('ul', {
    id: 'package-suggestions',
    class: 'picker-list',
    role: 'listbox',
    'aria-label': 'Package suggestions',
    hidden: true,
  });

  const status = h('span', { class: 'picker-status', role: 'status', 'aria-live': 'polite' });

  const versionSelect = h(
    'select',
    { id: 'version-input', 'aria-label': 'Package version' },
    h('option', { value: '', text: 'latest' }),
  );

  const element = document.createDocumentFragment();
  element.append(
    h(
      'div',
      { class: 'picker-field' },
      h(
        'div',
        { class: 'field' },
        h('label', { for: 'package-input', text: 'Package' }),
        h('div', { class: 'picker' }, input, list),
      ),
      status,
    ),
    h(
      'div',
      { class: 'field picker-version-field' },
      h('label', { for: 'version-input', text: 'Version' }),
      versionSelect,
    ),
  );

  let scanHandler: ((packageName: string, version: string) => void) | null = null;
  let suggestions: Suggestion[] = [];
  let activeIndex = -1;
  let lookupTimer: ReturnType<typeof setTimeout> | null = null;
  let lookupToken = 0;

  const closeList = (): void => {
    list.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    activeIndex = -1;
  };

  const setStatus = (text: string, title = ''): void => {
    status.textContent = text;
    status.title = title;
    status.classList.toggle('is-error', !text && Boolean(title));
  };

  const renderSuggestions = (items: Suggestion[]): void => {
    suggestions = items;
    activeIndex = -1;
    if (!items.length) {
      closeList();
      return;
    }
    list.replaceChildren(
      ...items.map((item, index) =>
        h(
          'li',
          {
            class: `picker-option${item.recent ? ' is-recent' : ''}`,
            role: 'option',
            id: `package-option-${index}`,
            'aria-selected': 'false',
            // mousedown rather than click: the input keeps the focus, so the
            // list does not close before the option is picked.
            onmousedown: (event: Event) => {
              event.preventDefault();
              choose(item);
            },
          },
          h('span', { class: 'picker-option-name', text: item.name }),
          item.version
            ? h('span', { class: 'picker-option-version', text: item.version })
            : null,
        ),
      ),
    );
    list.hidden = false;
    input.setAttribute('aria-expanded', 'true');
  };

  const highlight = (index: number): void => {
    const options = Array.from(list.querySelectorAll<HTMLElement>('.picker-option'));
    options.forEach((option, i) => {
      const active = i === index;
      option.classList.toggle('is-active', active);
      option.setAttribute('aria-selected', String(active));
    });
    if (index >= 0) {
      input.setAttribute('aria-activedescendant', `package-option-${index}`);
    } else {
      input.removeAttribute('aria-activedescendant');
    }
    activeIndex = index;
  };

  const populateVersions = (versions: string[]): void => {
    versionSelect.replaceChildren(
      h('option', { value: '', text: 'latest' }),
      ...versions.map((version) => h('option', { value: version, text: version })),
    );
  };

  const resolve = async (name: string): Promise<void> => {
    const token = ++lookupToken;
    if (!name) {
      setStatus('');
      populateVersions([]);
      return;
    }
    const lookup = await controller.lookupPackage(name);
    if (token !== lookupToken) return; // a newer lookup won
    if (!lookup) {
      setStatus('', 'the package index is unreachable');
      return;
    }
    if (lookup.error) {
      setStatus(`${lookup.name} was not found`, lookup.error);
      populateVersions([]);
      return;
    }
    const title = lookup.summary ?? '';
    setStatus(lookup.latest ? `${lookup.name} ${lookup.latest}` : lookup.name, title);
    populateVersions(lookup.versions ?? []);
  };

  const scheduleResolve = (): void => {
    if (lookupTimer) clearTimeout(lookupTimer);
    lookupTimer = setTimeout(() => {
      lookupTimer = null;
      void resolve(input.value.trim());
    }, LOOKUP_DELAY_MS);
  };

  const choose = (item: Suggestion): void => {
    input.value = item.name;
    closeList();
    void resolve(item.name);
  };

  const knownPackages = (): Suggestion[] => {
    const seen = new Set<string>();
    const out: Suggestion[] = [];
    for (const audit of useAuditStore.getState().audits) {
      const key = audit.package.toLowerCase();
      if (!key || seen.has(key)) continue;
      seen.add(key);
      out.push({ name: audit.package, version: audit.version ?? '', recent: true });
    }
    return out;
  };

  const updateSuggestions = (): void => {
    const query = input.value.trim().toLowerCase();
    const recent = knownPackages();
    const popular: Suggestion[] = POPULAR_PACKAGES.map((name) => ({
      name,
      version: '',
      recent: false,
    }));

    // A package that was audited before is offered once, with the version
    // it was audited at.
    const recentNames = new Set(recent.map((item) => item.name.toLowerCase()));
    const fresh = popular.filter((item) => !recentNames.has(item.name.toLowerCase()));

    let items: Suggestion[];
    if (!query) {
      items = [...recent, ...fresh];
    } else {
      const starts = fresh.filter((item) => item.name.toLowerCase().startsWith(query));
      const contains = fresh.filter(
        (item) =>
          !item.name.toLowerCase().startsWith(query) &&
          item.name.toLowerCase().includes(query),
      );
      const matchedRecent = recent.filter((item) => item.name.toLowerCase().includes(query));
      items = [...matchedRecent, ...starts, ...contains];
    }
    renderSuggestions(items.slice(0, MAX_SUGGESTIONS));
  };

  const submit = (): void => {
    const name = input.value.trim();
    if (!name) {
      input.focus();
      setStatus('', 'enter a package name');
      return;
    }
    closeList();
    scanHandler?.(name, versionSelect.value);
  };

  input.addEventListener('input', () => {
    updateSuggestions();
    scheduleResolve();
  });

  input.addEventListener('focus', () => {
    updateSuggestions();
  });

  input.addEventListener('blur', () => {
    // The list is closed on the next tick so a click on an option still
    // reaches it.
    setTimeout(closeList, 120);
  });

  input.addEventListener('keydown', (event: KeyboardEvent) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      if (list.hidden || !suggestions.length) return;
      event.preventDefault();
      const delta = event.key === 'ArrowDown' ? 1 : -1;
      const next = activeIndex + delta;
      highlight(next < 0 ? suggestions.length - 1 : next % suggestions.length);
      return;
    }
    if (event.key === 'Escape') {
      if (!list.hidden) {
        event.preventDefault();
        closeList();
      }
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      if (activeIndex >= 0 && suggestions[activeIndex]) {
        choose(suggestions[activeIndex]);
        return;
      }
      submit();
    }
  });

  versionSelect.addEventListener('change', () => {
    const value = versionSelect.value;
    const name = input.value.trim();
    setStatus(value ? `${name} ${value}` : name ? `${name} (latest)` : '');
  });

  return {
    element,
    packageName: () => input.value.trim(),
    version: () => versionSelect.value,
    onScan: (handler) => {
      scanHandler = handler;
    },
    focus: () => input.focus(),
    dispose: () => {
      if (lookupTimer) clearTimeout(lookupTimer);
      lookupTimer = null;
      scanHandler = null;
    },
  };
}
