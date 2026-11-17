import {PLUGIN_ID} from './types';
import type {ScheduledMessage} from './types';

export const OPEN_SCHEDULE_MODAL = `${PLUGIN_ID}/OPEN_SCHEDULE_MODAL`;
export const CLOSE_SCHEDULE_MODAL = `${PLUGIN_ID}/CLOSE_SCHEDULE_MODAL`;
export const OPEN_SCHEDULED_LIST = `${PLUGIN_ID}/OPEN_SCHEDULED_LIST`;
export const CLOSE_SCHEDULED_LIST = `${PLUGIN_ID}/CLOSE_SCHEDULED_LIST`;

export type OpenScheduleModalAction = {
    type: typeof OPEN_SCHEDULE_MODAL;
    initialMessage?: string;
    editing?: ScheduledMessage;
    convertOnline?: boolean;
    fileIds?: string[];
    fileNames?: string[];
    rootId?: string;
    channelId?: string;
};
export type CloseScheduleModalAction = {type: typeof CLOSE_SCHEDULE_MODAL};
export type OpenScheduledListAction = {type: typeof OPEN_SCHEDULED_LIST};
export type CloseScheduledListAction = {type: typeof CLOSE_SCHEDULED_LIST};

type Action =
    | OpenScheduleModalAction
    | CloseScheduleModalAction
    | OpenScheduledListAction
    | CloseScheduledListAction;

export type PluginState = {
    modalOpen: boolean;
    listOpen: boolean;
    initialMessage: string;
    editing: ScheduledMessage | null;
    convertOnline: boolean;
    fileIds: string[];
    fileNames: string[];
    rootId: string;
    channelId: string;
};

const initialState: PluginState = {
    modalOpen: false,
    listOpen: false,
    initialMessage: '',
    editing: null,
    convertOnline: false,
    fileIds: [],
    fileNames: [],
    rootId: '',
    channelId: '',
};

export const reducer = (state: PluginState = initialState, action: Action): PluginState => {
    switch (action.type) {
    case OPEN_SCHEDULE_MODAL:
        return {
            ...state,
            modalOpen: true,
            initialMessage: action.initialMessage ?? '',
            editing: action.editing ?? null,
            convertOnline: action.convertOnline ?? false,
            fileIds: action.editing ? (action.editing.file_ids ?? []) : (action.fileIds ?? []),
            fileNames: action.editing ? [] : (action.fileNames ?? []),
            rootId: action.editing ? (action.editing.root_id ?? '') : (action.rootId ?? ''),
            channelId: action.editing ? '' : (action.channelId ?? ''),
        };
    case CLOSE_SCHEDULE_MODAL:
        return {...initialState, listOpen: state.listOpen};
    case OPEN_SCHEDULED_LIST:
        return {...state, listOpen: true};
    case CLOSE_SCHEDULED_LIST:
        return {...state, listOpen: false};
    default:
        return state;
    }
};

export const openScheduleModal = (
    initialMessage = '',
    draft?: {fileIds?: string[]; fileNames?: string[]; rootId?: string; channelId?: string},
): OpenScheduleModalAction => ({
    type: OPEN_SCHEDULE_MODAL,
    initialMessage,
    fileIds: draft?.fileIds,
    fileNames: draft?.fileNames,
    rootId: draft?.rootId,
    channelId: draft?.channelId,
});
export const openScheduleModalForEdit = (msg: ScheduledMessage, convertOnline = false): OpenScheduleModalAction => ({
    type: OPEN_SCHEDULE_MODAL,
    editing: msg,
    convertOnline,
});
export const closeScheduleModal = (): CloseScheduleModalAction => ({type: CLOSE_SCHEDULE_MODAL});
export const openScheduledList = (): OpenScheduledListAction => ({type: OPEN_SCHEDULED_LIST});
export const closeScheduledList = (): CloseScheduledListAction => ({type: CLOSE_SCHEDULED_LIST});

// Plugin reducers can be mounted under either `state['plugins-<id>']` or
// `state.plugins[<id>]` depending on Mattermost webapp version — try both.
export const selectPluginState = (state: any): PluginState => {
    const slice =
        state?.[`plugins-${PLUGIN_ID}`] ??
        state?.plugins?.[PLUGIN_ID] ??
        state?.plugins?.plugins?.[PLUGIN_ID];
    if (!slice) {
        return initialState;
    }
    return {
        ...initialState,
        ...slice,
        fileIds: slice.fileIds ?? [],
        fileNames: slice.fileNames ?? [],
        rootId: slice.rootId ?? '',
        channelId: slice.channelId ?? '',
    };
};
