import type { NavView } from '$lib/types';

function loadBool(key: string, fallback: boolean): boolean {
	if (typeof localStorage === 'undefined') return fallback;
	const v = localStorage.getItem(key);
	if (v === null) return fallback;
	return v === 'true';
}

function loadNumber(key: string, fallback: number): number {
	if (typeof localStorage === 'undefined') return fallback;
	const v = localStorage.getItem(key);
	if (v === null) return fallback;
	const n = parseFloat(v);
	return isNaN(n) ? fallback : n;
}

function loadString(key: string, fallback: string): string {
	if (typeof localStorage === 'undefined') return fallback;
	return localStorage.getItem(key) ?? fallback;
}

export const navState = $state({
	view: 'dashboard' as NavView,
	sidebarCollapsed: loadBool('ui:sidebar-collapsed', false),
	sidebarMobileOpen: false,
	chatPanelOpen: loadBool('ui:chat-panel-open', true),
	boardWidth: loadNumber('ui:board-width', 60),
	focusedColumn: loadNumber('ui:focused-column', 0),
	focusedRow: loadNumber('ui:focused-row', 0),
});

// Persist helpers — write on change
function persist(key: string, value: string) {
	if (typeof localStorage !== 'undefined') localStorage.setItem(key, value);
}

export function navigate(view: NavView) {
	navState.view = view;
	navState.sidebarMobileOpen = false;
}

export function toggleSidebar() {
	navState.sidebarCollapsed = !navState.sidebarCollapsed;
	persist('ui:sidebar-collapsed', String(navState.sidebarCollapsed));
}

export function toggleMobileSidebar() {
	navState.sidebarMobileOpen = !navState.sidebarMobileOpen;
}

export function closeMobileSidebar() {
	navState.sidebarMobileOpen = false;
}

export function toggleChatPanel() {
	navState.chatPanelOpen = !navState.chatPanelOpen;
	persist('ui:chat-panel-open', String(navState.chatPanelOpen));
}

export function setBoardWidth(width: number) {
	navState.boardWidth = width;
	persist('ui:board-width', String(width));
}

export function setFocus(column: number, row: number) {
	navState.focusedColumn = column;
	navState.focusedRow = row;
	persist('ui:focused-column', String(column));
	persist('ui:focused-row', String(row));
}
