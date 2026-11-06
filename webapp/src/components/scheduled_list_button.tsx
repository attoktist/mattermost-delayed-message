import React, {useEffect, useRef, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {cancelScheduled, fetchScheduledForChannel, sendScheduledNow} from '../client';
import {emitScheduledChanged, SCHEDULED_CHANGED_EVENT} from '../events';
import {openScheduleModalForEdit} from '../redux';
import type {ScheduledMessage} from '../types';

type Draft = {
    message: string;
    channelId: string;
};

type Props = {
    draft: Draft;
};

type Anchor = {left: number; top: number; downward: boolean};
type MenuPoint = {id: string; left: number; top: number};

const whenLabel = (message: ScheduledMessage): string => {
    if (message.trigger === 'online') {
        return 'Когда будет в сети';
    }
    if (!message.send_at) {
        return '';
    }
    const tz = message.tz || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    try {
        return new Intl.DateTimeFormat('ru', {
            day: 'numeric',
            month: 'short',
            hour: '2-digit',
            minute: '2-digit',
            timeZone: tz,
        }).format(new Date(message.send_at));
    } catch {
        return '';
    }
};

const ScheduledListButton: React.FC<Props> = ({draft}) => {
    const dispatch = useDispatch();
    const editing = useSelector((state: any) => Boolean(state?.views?.posts?.editingPost?.show));
    const [items, setItems] = useState<ScheduledMessage[]>([]);
    const [open, setOpen] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [anchor, setAnchor] = useState<Anchor | null>(null);
    const [menu, setMenu] = useState<MenuPoint | null>(null);
    const rootRef = useRef<HTMLDivElement>(null);
    const buttonRef = useRef<HTMLButtonElement>(null);
    const busyRef = useRef(false);

    useEffect(() => {
        if (editing) {
            setOpen(false);
            setMenu(null);
        }
    }, [editing]);

    useEffect(() => {
        if (editing || !draft?.channelId) {
            return undefined;
        }
        let cancelled = false;
        setItems([]);
        const load = async () => {
            try {
                const data = await fetchScheduledForChannel(draft.channelId);
                if (!cancelled) {
                    setItems(data);
                    setError(null);
                }
            } catch (e: any) {
                if (!cancelled) {
                    setError(e?.message ?? 'Не удалось загрузить');
                }
            }
        };
        load();
        const timer = window.setInterval(load, 30_000);
        window.addEventListener(SCHEDULED_CHANGED_EVENT, load);
        return () => {
            cancelled = true;
            window.clearInterval(timer);
            window.removeEventListener(SCHEDULED_CHANGED_EVENT, load);
        };
    }, [draft?.channelId, editing]);

    useEffect(() => {
        if (!open && !menu) {
            return undefined;
        }
        const onDoc = (event: MouseEvent) => {
            if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
                setOpen(false);
                setMenu(null);
            }
        };
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                setMenu(null);
                setOpen(false);
            }
        };
        document.addEventListener('mousedown', onDoc);
        document.addEventListener('keydown', onKey);
        return () => {
            document.removeEventListener('mousedown', onDoc);
            document.removeEventListener('keydown', onKey);
        };
    }, [open, menu]);

    if (editing || !draft) {
        return null;
    }

    const toggle = () => {
        if (open) {
            setOpen(false);
            setMenu(null);
            return;
        }
        const rect = buttonRef.current?.getBoundingClientRect();
        if (rect) {
            setAnchor({left: rect.left, top: rect.top, downward: rect.top < 220});
        }
        setOpen(true);
    };

    const selected = menu ? items.find((item) => item.id === menu.id) : undefined;

    const edit = (message: ScheduledMessage, convertOnline: boolean) => {
        setOpen(false);
        setMenu(null);
        dispatch(openScheduleModalForEdit(message, convertOnline));
    };

    const copyText = async (message: ScheduledMessage) => {
        try {
            await navigator.clipboard.writeText(message.message);
            setMenu(null);
            setError(null);
        } catch (e: any) {
            setError(e?.message ?? 'Не удалось скопировать текст');
        }
    };

    const mutate = async (action: () => Promise<void>, fallback: string) => {
        if (busyRef.current) {
            return;
        }
        busyRef.current = true;
        try {
            await action();
            emitScheduledChanged();
            setMenu(null);
            setError(null);
        } catch (e: any) {
            setError(e?.message ?? fallback);
        } finally {
            busyRef.current = false;
        }
    };

    const panelStyle: React.CSSProperties = {
        position: 'fixed',
        left: anchor?.left ?? 0,
        top: anchor?.downward ? (anchor.top + 36) : (anchor?.top ?? 0),
        transform: anchor?.downward ? undefined : 'translateY(calc(-100% - 4px))',
        zIndex: 2000,
        width: 320,
        maxHeight: 280,
        overflowY: 'auto',
        padding: 4,
        background: 'var(--center-channel-bg, #fff)',
        color: 'var(--center-channel-color, #1c1c1c)',
        border: '1px solid rgba(63,67,80,0.16)',
        borderRadius: 4,
        boxShadow: '0 8px 24px rgba(0,0,0,0.16)',
    };

    const contextStyle: React.CSSProperties = {
        position: 'fixed',
        left: menu ? Math.min(menu.left, window.innerWidth - 220) : 0,
        top: menu ? Math.min(menu.top, window.innerHeight - 200) : 0,
        zIndex: 2001,
        minWidth: 200,
        padding: 4,
        background: 'var(--center-channel-bg, #fff)',
        color: 'var(--center-channel-color, #1c1c1c)',
        border: '1px solid rgba(63,67,80,0.16)',
        borderRadius: 4,
        boxShadow: '0 8px 24px rgba(0,0,0,0.16)',
    };

    return (
        <div
            ref={rootRef}
            style={wrapStyle}
        >
            <button
                ref={buttonRef}
                type='button'
                aria-label='Отложенные сообщения канала'
                aria-expanded={open}
                onClick={toggle}
                style={buttonStyle}
            >
                <ListIcon/>
                {items.length > 0 && <span style={dotStyle}/>}
            </button>
            {open && anchor && (
                <div
                    style={panelStyle}
                    onMouseDown={(event) => {
                        if (event.button === 0) {
                            setMenu(null);
                        }
                    }}
                >
                    {error && <div style={errorStyle}>{error}</div>}
                    {items.length === 0 && !error && (
                        <div style={emptyStyle}>{'В этом канале нет отложенных сообщений.'}</div>
                    )}
                    {items.map((item) => (
                        <div
                            key={item.id}
                            style={rowStyle}
                            onContextMenu={(event) => {
                                event.preventDefault();
                                setMenu({id: item.id, left: event.clientX, top: event.clientY});
                            }}
                        >
                            <div style={messageStyle}>{item.message}</div>
                            <div style={whenStyle}>{whenLabel(item)}</div>
                        </div>
                    ))}
                </div>
            )}
            {menu && selected && (
                <div
                    role='menu'
                    style={contextStyle}
                >
                    <button type='button' role='menuitem' style={itemStyle} onClick={() => edit(selected, false)}>
                        {'Изменить'}
                    </button>
                    <button type='button' role='menuitem' style={itemStyle} onClick={() => copyText(selected)}>
                        {'Копировать текст'}
                    </button>
                    <button type='button' role='menuitem' style={itemStyle} onClick={() => mutate(() => sendScheduledNow(selected.id), 'Не удалось отправить')}>
                        {'Отправить сейчас'}
                    </button>
                    <button type='button' role='menuitem' style={itemStyle} onClick={() => mutate(() => cancelScheduled(selected.id), 'Не удалось удалить')}>
                        {'Удалить'}
                    </button>
                    <button
                        type='button'
                        role='menuitem'
                        style={itemStyle}
                        onClick={() => edit(selected, selected.trigger === 'online')}
                    >
                        {'Изменить время'}
                    </button>
                </div>
            )}
        </div>
    );
};

const ListIcon: React.FC = () => (
    <svg
        width='18'
        height='18'
        viewBox='0 0 24 24'
        fill='none'
        xmlns='http://www.w3.org/2000/svg'
        aria-hidden='true'
        focusable='false'
    >
        <path d='M8 7h11M8 12h11M8 17h11' stroke='currentColor' strokeWidth='1.8' strokeLinecap='round'/>
        <circle cx='4.5' cy='7' r='1.2' fill='currentColor'/>
        <circle cx='4.5' cy='12' r='1.2' fill='currentColor'/>
        <circle cx='4.5' cy='17' r='1.2' fill='currentColor'/>
    </svg>
);

const wrapStyle: React.CSSProperties = {
    position: 'relative',
    display: 'inline-flex',
    alignItems: 'center',
};

const buttonStyle: React.CSSProperties = {
    position: 'relative',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 32,
    height: 32,
    padding: 0,
    border: 'none',
    borderRadius: 4,
    background: 'transparent',
    color: 'inherit',
    cursor: 'pointer',
};

const dotStyle: React.CSSProperties = {
    position: 'absolute',
    top: 6,
    right: 5,
    width: 8,
    height: 8,
    borderRadius: '50%',
    background: 'var(--dnd-indicator, #d24b4e)',
};

const rowStyle: React.CSSProperties = {
    padding: '8px 10px',
    borderRadius: 3,
    cursor: 'context-menu',
};

const messageStyle: React.CSSProperties = {
    fontSize: 13,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
};

const whenStyle: React.CSSProperties = {
    marginTop: 2,
    fontSize: 11,
    opacity: 0.7,
};

const emptyStyle: React.CSSProperties = {
    padding: '8px 10px',
    fontSize: 13,
    opacity: 0.7,
};

const errorStyle: React.CSSProperties = {
    padding: '6px 10px',
    fontSize: 12,
    color: '#d24b4e',
};

const itemStyle: React.CSSProperties = {
    display: 'block',
    width: '100%',
    textAlign: 'left',
    padding: '8px 10px',
    border: 'none',
    borderRadius: 3,
    background: 'transparent',
    color: 'inherit',
    fontSize: 14,
    cursor: 'pointer',
};

export default ScheduledListButton;
