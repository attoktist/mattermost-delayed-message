import React from 'react';

import Root from './components/Root';
import ScheduledCountBadge from './components/ScheduledCountBadge';
import ClockIcon from './components/ClockIcon';
import ScheduleMenuButton from './components/schedule_menu_button';
import ScheduledListButton from './components/scheduled_list_button';
import {openScheduleModal, openScheduledList, reducer} from './redux';
import {PLUGIN_ID} from './types';

type Registry = {
    registerReducer?: (r: any) => void;
    registerRootComponent?: (c: any) => void;
    registerChannelHeaderButtonAction?: (
        icon: React.ReactNode,
        action: () => void,
        dropdownText: string,
        tooltipText: string,
    ) => string;
    registerLeftSidebarHeaderComponent?: (c: any) => void;
    registerChannelHeaderMenuAction?: (text: string, action: () => void) => string;
    registerPostEditorActionComponent: (component: React.ComponentType<any>) => string;
};

class Plugin {
    public initialize(registry: Registry, store: {dispatch: (a: any) => void; getState: () => any}) {
        if (registry.registerReducer) {
            registry.registerReducer(reducer);
        }

        if (registry.registerRootComponent) {
            registry.registerRootComponent(Root as any);
        }

        // Channel header button: a clock icon at the top-right of every channel
        // that opens the scheduling modal.
        if (registry.registerChannelHeaderButtonAction) {
            registry.registerChannelHeaderButtonAction(
                React.createElement(ClockIcon, {size: 16}),
                () => store.dispatch(openScheduleModal('')),
                'Отложить сообщение',
                'Отложить сообщение',
            );
        }

        // Channel header dropdown entry to open the pending-list view, so users
        // can see/cancel scheduled messages without the left sidebar entry.
        if (registry.registerChannelHeaderMenuAction) {
            registry.registerChannelHeaderMenuAction(
                'Отложенные сообщения',
                () => store.dispatch(openScheduledList()),
            );
        }

        // Left sidebar entry for the pending-list view (with count badge).
        if (registry.registerLeftSidebarHeaderComponent) {
            registry.registerLeftSidebarHeaderComponent(ScheduledCountBadge as any);
        }

        registry.registerPostEditorActionComponent(ScheduleMenuButton);
        registry.registerPostEditorActionComponent(ScheduledListButton);
    }
}

declare global {
    interface Window {
        registerPlugin: (id: string, plugin: Plugin) => void;
    }
}

window.registerPlugin(PLUGIN_ID, new Plugin());
