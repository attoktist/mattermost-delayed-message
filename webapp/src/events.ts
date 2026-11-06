import {PLUGIN_ID} from './types';

// Custom DOM event emitted whenever the user creates or cancels a scheduled
// message. The sidebar count badge listens on `window` and reloads — using a
// DOM event sidesteps redux Provider issues across plugin-mounted components.
export const SCHEDULED_CHANGED_EVENT = `${PLUGIN_ID}:scheduled-changed`;

export const emitScheduledChanged = (): void => {
    try {
        window.dispatchEvent(new CustomEvent(SCHEDULED_CHANGED_EVENT));
    } catch {
        // older browsers / non-DOM environments — ignore
    }
};

// The composer menu hands its draft to the schedule modal. Only that handoff
// may clear the field, and only via the same updateText("") the presence item uses.
// The channel-header clock opens the modal with an empty draft and must not register.
let composerDraftClear: (() => void) | null = null;

export const handComposerDraft = (clear: () => void): void => {
    composerDraftClear = clear;
};

export const releaseComposerDraft = (): void => {
    composerDraftClear = null;
};

export const consumeComposerDraft = (): void => {
    const clear = composerDraftClear;
    composerDraftClear = null;
    clear?.();
};
