/**
 * The accents the user can pick: the signal colour of the interface.
 *
 * `ground` is the hue of the ground — the surfaces, the borders and the
 * text — which follows the accent, so the app reads as one colour family
 * rather than a green app with a blue button. Lime keeps the house
 * Verdigris ink it was designed on; the others take the hue of their own
 * accent.
 */

import { t } from './i18n';

export const ACCENTS = [
  { id: 'lime', label: () => t('accent.lime'), dark: ['#b4f23c', '#c8ff5a', '#232f16'], light: ['#3f6212', '#2f4c0c', '#d5e6bb'], ground: 172 },
  { id: 'teal', label: () => t('accent.teal'), dark: ['#5fd3c4', '#8ae9dc', '#17302e'], light: ['#0b5f59', '#094845', '#c4e6e1'], ground: 172 },
  { id: 'blue', label: () => t('accent.blue'), dark: ['#6cb2ff', '#9acaff', '#16283d'], light: ['#1e40af', '#172f85', '#cdd9f7'], ground: 212 },
  { id: 'violet', label: () => t('accent.violet'), dark: ['#b79cff', '#d0bcff', '#2a2142'], light: ['#5b21b6', '#46188f', '#dccff5'], ground: 256 },
  { id: 'orange', label: () => t('accent.orange'), dark: ['#ffa24a', '#ffbb7a', '#38261a'], light: ['#9a3412', '#7a280d', '#f3d4bf'], ground: 29 },
] as const;

export type AccentId = (typeof ACCENTS)[number]['id'];
