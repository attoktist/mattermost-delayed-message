import React, {useEffect, useRef, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {createWhenOnline} from '../client';
import {emitScheduledChanged, handComposerDraft} from '../events';
import {openScheduleModal} from '../redux';
import ClockIcon from './ClockIcon';

type Draft = {
    message: string;
    channelId: string;
    fileInfos?: readonly unknown[];
    uploadsInProgress?: readonly unknown[];
};

type Props = {
    draft: Draft;
    updateText: (message: string) => void;
};

const ScheduleMenuButton: React.FC<Props> = ({draft, updateText}) => {
    const dispatch = useDispatch();
    const editing = useSelector((state: any) => Boolean(state?.views?.posts?.editingPost?.show));
    const channelType = useSelector((state: any) => (
        state?.entities?.channels?.channels?.[draft?.channelId]?.type as string | undefined
    ));
    const [open, setOpen] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const busyRef = useRef(false);
    const [anchor, setAnchor] = useState<{left: number; top: number; downward: boolean} | null>(null);
    const rootRef = useRef<HTMLDivElement>(null);
    const buttonRef = useRef<HTMLButtonElement>(null);

    useEffect(() => {
        if (!open) {
            return undefined;
        }
        const onDoc = (event: MouseEvent) => {
            if (rootRef.current && !rootRef.current.contains(event.target as Node)) {
                setOpen(false);
            }
        };
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                setOpen(false);
            }
        };
        document.addEventListener('mousedown', onDoc);
        document.addEventListener('keydown', onKey);
        return () => {
            document.removeEventListener('mousedown', onDoc);
            document.removeEventListener('keydown', onKey);
        };
    }, [open]);

    if (editing || !draft) {
        return null;
    }

    const hasFiles = (draft.fileInfos?.length ?? 0) > 0 || (draft.uploadsInProgress?.length ?? 0) > 0;
    const isDirect = channelType === 'D';

    const toggle = () => {
        if (open) {
            setOpen(false);
            return;
        }
        const rect = buttonRef.current?.getBoundingClientRect();
        if (rect) {
            setAnchor({left: rect.left, top: rect.top, downward: rect.top < 160});
        }
        setError(null);
        setOpen(true);
    };

    const onLater = () => {
        if (hasFiles || !draft.message.trim()) {
            return;
        }
        handComposerDraft(() => updateText(''));
        dispatch(openScheduleModal(draft.message));
        setOpen(false);
    };

    const onOnline = async () => {
        if (hasFiles || busyRef.current || !draft.message.trim()) {
            return;
        }
        busyRef.current = true;
        setError(null);
        try {
            await createWhenOnline(draft.channelId, draft.message);
            updateText('');
            emitScheduledChanged();
            setOpen(false);
        } catch (e: any) {
            setError(e?.message ?? 'Не удалось отложить');
        } finally {
            busyRef.current = false;
        }
    };

    const menuStyle: React.CSSProperties = {
        position: 'fixed',
        left: anchor?.left ?? 0,
        top: anchor?.downward ? (anchor.top + 36) : (anchor?.top ?? 0),
        transform: anchor?.downward ? undefined : 'translateY(calc(-100% - 4px))',
        zIndex: 2000,
        minWidth: 240,
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
                aria-label='Отложить сообщение'
                aria-expanded={open}
                aria-haspopup='menu'
                onClick={toggle}
                style={buttonStyle}
            >
                <ClockIcon size={18}/>
            </button>
            {open && anchor && (
                <div
                    role='menu'
                    style={menuStyle}
                >
                    {hasFiles && <div style={warnStyle}>{'Вложения отложить нельзя.'}</div>}
                    <button
                        type='button'
                        role='menuitem'
                        onClick={onLater}
                        style={itemStyle}
                    >
                        {'Отправить позже'}
                    </button>
                    {isDirect && (
                        <button
                            type='button'
                            role='menuitem'
                            onClick={onOnline}
                            style={itemStyle}
                        >
                            {'Когда будет в сети'}
                        </button>
                    )}
                    {error && <div style={errorStyle}>{error}</div>}
                </div>
            )}
        </div>
    );
};

const wrapStyle: React.CSSProperties = {
    position: 'relative',
    display: 'inline-flex',
    alignItems: 'center',
};

const buttonStyle: React.CSSProperties = {
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

const warnStyle: React.CSSProperties = {
    padding: '8px 10px',
    fontSize: 13,
    color: '#d24b4e',
};

const errorStyle: React.CSSProperties = {
    padding: '6px 10px',
    fontSize: 12,
    color: '#d24b4e',
};

export default ScheduleMenuButton;
