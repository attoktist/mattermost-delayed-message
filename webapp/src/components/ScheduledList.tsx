import React, {useCallback, useEffect, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {cancelScheduled, fetchScheduled} from '../client';
import {emitScheduledChanged} from '../events';
import {closeScheduledList, openScheduleModal, openScheduleModalForEdit, selectPluginState} from '../redux';
import type {ScheduledMessage} from '../types';

const repeatLabel = (repeat: string): string => {
    switch (repeat) {
    case 'daily':
        return 'каждый день';
    case 'weekdays':
        return 'по будням';
    case 'weekly':
        return 'каждую неделю';
    case 'fortnightly':
        return 'раз в две недели';
    case 'monthly':
        return 'каждый месяц';
    case 'yearly':
        return 'каждый год';
    default:
        return repeat;
    }
};

const statusLabel = (status: string): string => {
    switch (status) {
    case 'pending':
        return 'ожидает';
    case 'sent':
        return 'отправлено';
    case 'failed':
        return 'ошибка';
    case 'completed':
        return 'завершено';
    default:
        return status;
    }
};

const formatLocal = (ms: number, tz: string): string => {
    const parts = new Intl.DateTimeFormat('ru', {
        weekday: 'short', day: 'numeric', month: 'short', year: 'numeric',
        hour: '2-digit', minute: '2-digit', hour12: false,
        timeZone: tz,
        timeZoneName: 'short',
    }).formatToParts(new Date(ms));
    const get = (t: string) => parts.find((p) => p.type === t)?.value ?? '';
    return `${get('weekday')} ${get('day')} ${get('month')} ${get('year')}, ${get('hour')}:${get('minute')} ${get('timeZoneName')}`;
};

const formatTimeOfDay = (ms: number, tz: string): string => {
    const parts = new Intl.DateTimeFormat('ru', {
        hour: '2-digit', minute: '2-digit', hour12: false, timeZone: tz,
    }).formatToParts(new Date(ms));
    const get = (t: string) => parts.find((p) => p.type === t)?.value ?? '';
    return `${get('hour')}:${get('minute')}`;
};

const formatWhen = (m: ScheduledMessage, tz: string): string => {
    if (m.trigger === 'online') {
        return 'Когда будет в сети';
    }
    const base = formatLocal(m.send_at, tz);
    if (!m.window_ms || m.window_ms <= 0) {
        return base;
    }
    return `${base} – ${formatTimeOfDay(m.send_at + m.window_ms, tz)} (случайно)`;
};

const describeRepeat = (m: ScheduledMessage, tz: string): string => {
    if (!m.repeat) {
        return '—';
    }
    const label = repeatLabel(m.repeat);
    if (m.ends_mode === 'on' && m.ends_at) {
        return `${label} до ${formatLocal(m.ends_at, tz)}`;
    }
    if (m.ends_mode === 'after' && m.ends_after) {
        const left = Math.max(0, m.ends_after - (m.occurrences ?? 0));
        return `${label} · осталось ${left}`;
    }
    return label;
};

// resolveUserTimezone reads the user's Mattermost-configured timezone from
// the redux store, falling back to the browser timezone.
const resolveUserTimezone = (state: any): string => {
    try {
        const userId = state?.entities?.users?.currentUserId;
        const profile = userId ? state?.entities?.users?.profiles?.[userId] : undefined;
        const tz = profile?.timezone;
        if (tz) {
            if (tz.useAutomaticTimezone === 'true' && tz.automaticTimezone) {
                return tz.automaticTimezone;
            }
            if (tz.manualTimezone) {
                return tz.manualTimezone;
            }
        }
    } catch {
        // fall through
    }
    try {
        return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch {
        return 'UTC';
    }
};

const ScheduledList: React.FC = () => {
    const dispatch = useDispatch();
    const {listOpen} = useSelector(selectPluginState);
    const channels = useSelector((s: any) => s?.entities?.channels?.channels as Record<string, {type?: string; display_name?: string; name?: string; teammate_id?: string}> | undefined);
    const profiles = useSelector((s: any) => s?.entities?.users?.profiles as Record<string, {username?: string; first_name?: string; last_name?: string; nickname?: string}> | undefined);
    const currentUserId = useSelector((s: any) => s?.entities?.users?.currentUserId as string | undefined);
    const userTz = useSelector(resolveUserTimezone);

    const [items, setItems] = useState<ScheduledMessage[]>([]);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const reload = useCallback(async () => {
        setLoading(true);
        setError(null);
        try {
            const data = await fetchScheduled();
            data.sort((a, b) => a.send_at - b.send_at);
            setItems(data);
        } catch (e: any) {
            setError(e?.message ?? 'Не удалось загрузить');
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        if (listOpen) {
            reload();
        }
    }, [listOpen, reload]);

    if (!listOpen) {
        return null;
    }

    const onCancel = async (id: string) => {
        try {
            await cancelScheduled(id);
            setItems((prev) => prev.filter((i) => i.id !== id));
            emitScheduledChanged();
        } catch (e: any) {
            setError(e?.message ?? 'Не удалось отменить');
        }
    };

    const channelLabel = (id: string) => {
        const ch = channels?.[id];
        if (ch?.type === 'D') {
            // For DMs the channel's display_name/name is the concatenated user IDs.
            // Always render the teammate as @username so it's visually distinct from channels.
            let teammateId = ch.teammate_id;
            if (!teammateId && ch.name && currentUserId) {
                const parts = ch.name.split('__');
                teammateId = parts.find((p) => p !== currentUserId) || parts[0];
            }
            const p = teammateId ? profiles?.[teammateId] : undefined;
            if (p?.username) {
                return `@${p.username}`;
            }
        }
        if (ch?.type === 'G' && ch.display_name) {
            // Mattermost builds GM display_name as a comma-separated list of usernames.
            // Prefix each with @ so users are visually distinct from channel names.
            return ch.display_name.split(',').map((s) => `@${s.trim()}`).join(', ');
        }
        return ch?.display_name || ch?.name || id;
    };

    // renderTargets shows the first label, with "+N more" when there are
    // additional targets. The full comma-joined list is exposed via the title
    // attribute so users get the complete picture on hover.
    const renderTargets = (ids: string[]) => {
        if (ids.length === 0) {
            return null;
        }
        const labels = ids.map(channelLabel);
        if (labels.length === 1) {
            return <span>{labels[0]}</span>;
        }
        if (labels.length === 2) {
            return (
                <span title={labels.join(', ')}>{labels.join(', ')}</span>
            );
        }
        return (
                <span title={labels.join(', ')}>
                {labels[0]}
                <span style={moreTargetsStyle}>{` +${labels.length - 1} ещё`}</span>
            </span>
        );
    };

    const onClose = () => dispatch(closeScheduledList());

    return (
        <div
            role='dialog'
            aria-modal='true'
            style={overlayStyle}
            onClick={onClose}
        >
            <div
                style={modalStyle}
                onClick={(e) => e.stopPropagation()}
            >
                <div style={{display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8}}>
                    <h3 style={{margin: 0}}>{'Отложенные сообщения'}</h3>
                    <div style={{display: 'flex', gap: 8}}>
                        <button
                            type='button'
                            className='btn btn-primary'
                            onClick={() => {
                                dispatch(closeScheduledList());
                                dispatch(openScheduleModal(''));
                            }}
                        >
                            {'Новое отложенное сообщение'}
                        </button>
                        <button
                            type='button'
                            className='btn btn-tertiary'
                            onClick={reload}
                            disabled={loading}
                        >
                            {loading ? 'Загрузка…' : 'Обновить'}
                        </button>
                    </div>
                </div>

                {error && <div style={errorStyle}>{error}</div>}

                {!loading && items.length === 0 && (
                    <div style={emptyStyle}>{'Нет ожидающих отложенных сообщений.'}</div>
                )}

                {items.length > 0 && (
                    <table style={tableStyle}>
                        <thead>
                            <tr>
                                <th style={thStyle}>{'Когда'}</th>
                                <th style={thStyle}>{'Канал'}</th>
                                <th style={thStyle}>{'Повтор'}</th>
                                <th style={thStyle}>{'Сообщение'}</th>
                                <th style={thStyle}>{'Статус'}</th>
                                <th style={thStyle}/>
                            </tr>
                        </thead>
                        <tbody>
                            {items.map((m) => {
                                const completed = m.status === 'completed';
                                const rowTz = m.tz || userTz;
                                return (
                                    <tr key={m.id} style={completed ? completedRowStyle : undefined}>
                                        <td style={tdStyle}>{formatWhen(m, rowTz)}</td>
                                        <td style={tdStyle}>{renderTargets(m.channel_ids ?? [])}</td>
                                        <td style={tdStyle}>{describeRepeat(m, rowTz)}</td>
                                        <td style={tdStyle}>
                                            {m.message.length > 80 ? m.message.slice(0, 80) + '…' : m.message}
                                            {m.messages && m.messages.length >= 2 && (
                                                <div style={rotationHintStyle}>
                                                    {`+ ${m.messages.length - 1} ещё (по очереди)`}
                                                </div>
                                            )}
                                        </td>
                                        <td style={tdStyle}>{statusLabel(m.status)}</td>
                                        <td style={tdStyle}>
                                            {!completed && (
                                                <div style={{display: 'flex', gap: 6}}>
                                                    <button
                                                        type='button'
                                                        className='btn btn-tertiary'
                                                        onClick={() => {
                                                            dispatch(closeScheduledList());
                                                            dispatch(openScheduleModalForEdit(m));
                                                        }}
                                                    >
                                                        {'Изменить'}
                                                    </button>
                                                    <button
                                                        type='button'
                                                        className='btn btn-tertiary'
                                                        onClick={() => onCancel(m.id)}
                                                    >
                                                        {'Отменить'}
                                                    </button>
                                                </div>
                                            )}
                                        </td>
                                    </tr>
                                );
                            })}
                        </tbody>
                    </table>
                )}

                <div style={{display: 'flex', justifyContent: 'flex-end', marginTop: 16}}>
                    <button
                        type='button'
                        className='btn btn-primary'
                        onClick={onClose}
                    >
                        {'Закрыть'}
                    </button>
                </div>
            </div>
        </div>
    );
};

const overlayStyle: React.CSSProperties = {
    position: 'fixed',
    inset: 0,
    background: 'rgba(0,0,0,0.45)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    zIndex: 10000,
};

const modalStyle: React.CSSProperties = {
    background: 'var(--center-channel-bg, #fff)',
    color: 'var(--center-channel-color, #1c1c1c)',
    padding: 24,
    borderRadius: 8,
    minWidth: 560,
    maxWidth: 800,
    width: '90%',
    maxHeight: '80vh',
    overflowY: 'auto',
    boxShadow: '0 10px 40px rgba(0,0,0,0.25)',
};

const tableStyle: React.CSSProperties = {
    width: '100%',
    marginTop: 16,
    borderCollapse: 'collapse',
    fontSize: 13,
};

const thStyle: React.CSSProperties = {
    textAlign: 'left',
    padding: '8px 6px',
    borderBottom: '1px solid rgba(63,67,80,0.16)',
    fontWeight: 600,
};

const tdStyle: React.CSSProperties = {
    padding: '8px 6px',
    borderBottom: '1px solid rgba(63,67,80,0.08)',
    verticalAlign: 'top',
};

const emptyStyle: React.CSSProperties = {
    marginTop: 24,
    padding: 16,
    background: 'rgba(63,67,80,0.04)',
    borderRadius: 4,
    textAlign: 'center',
    fontSize: 13,
    color: 'rgba(63,67,80,0.72)',
};

const errorStyle: React.CSSProperties = {
    marginTop: 12,
    color: '#d24b4e',
    fontSize: 13,
};

const completedRowStyle: React.CSSProperties = {
    opacity: 0.55,
    fontStyle: 'italic',
};

const rotationHintStyle: React.CSSProperties = {
    fontSize: 11,
    color: 'rgba(63,67,80,0.6)',
    marginTop: 2,
};

const moreTargetsStyle: React.CSSProperties = {
    color: 'rgba(63,67,80,0.6)',
};

export default ScheduledList;
