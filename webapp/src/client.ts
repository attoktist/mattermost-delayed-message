import {PLUGIN_ID, ScheduledMessage, CreatePayload, UpdatePayload} from './types';

const BASE = `/plugins/${PLUGIN_ID}/api`;

function csrfToken(): string {
    const match = document.cookie.match(/(?:^|;\s*)MMCSRF=([^;]+)/);
    return match ? decodeURIComponent(match[1]) : '';
}

async function request<T>(
    path: string,
    init: RequestInit = {},
    parseJson = true,
): Promise<{status: number; data: T}> {
    const headers = new Headers(init.headers);
    headers.set('X-Requested-With', 'XMLHttpRequest');
    if (init.method && init.method !== 'GET') {
        headers.set('X-CSRF-Token', csrfToken());
        if (init.body && !headers.has('Content-Type')) {
            headers.set('Content-Type', 'application/json');
        }
    }

    const res = await fetch(`${BASE}${path}`, {
        credentials: 'include',
        ...init,
        headers,
    });

    if (!res.ok) {
        const text = await res.text().catch(() => '');
        throw new Error(text || `${res.status} ${res.statusText}`);
    }

    if (!parseJson || res.status === 204) {
        return {status: res.status, data: undefined as unknown as T};
    }
    return {status: res.status, data: await res.json() as T};
}

export const fetchScheduled = async (): Promise<ScheduledMessage[]> =>
    (await request<ScheduledMessage[]>('/list', {method: 'GET'})).data;

export const fetchScheduledForChannel = async (channelId: string): Promise<ScheduledMessage[]> =>
    (await request<ScheduledMessage[]>(`/list?channel_id=${encodeURIComponent(channelId)}`, {method: 'GET'})).data;

export const createScheduled = async (payload: CreatePayload): Promise<ScheduledMessage> =>
    (await request<ScheduledMessage>('/create', {
        method: 'POST',
        body: JSON.stringify(payload),
    })).data;

// Online create must not send send_at or peer_id. Draft clears only on 201.
export const createWhenOnline = async (channelId: string, message: string): Promise<void> => {
    const {status} = await request<ScheduledMessage>('/create', {
        method: 'POST',
        body: JSON.stringify({
            trigger: 'online',
            channel_ids: [channelId],
            message,
        }),
    });
    if (status !== 201) {
        throw new Error('не удалось отложить');
    }
};

export const updateScheduled = async (payload: UpdatePayload): Promise<ScheduledMessage> =>
    (await request<ScheduledMessage>('/update', {
        method: 'PATCH',
        body: JSON.stringify(payload),
    })).data;

export const sendScheduledNow = async (id: string): Promise<void> => {
    await request<void>('/send', {
        method: 'POST',
        body: JSON.stringify({id}),
    }, false);
};

export const cancelScheduled = async (id: string): Promise<void> => {
    await request<void>('/cancel', {
        method: 'DELETE',
        body: JSON.stringify({id}),
    }, false);
};
