import React, {useEffect, useRef, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';

import {createWhenOnline} from '../client';
import {emitScheduledChanged, handComposerDraft} from '../events';
import {openScheduleModal} from '../redux';
import ClockIcon from './ClockIcon';
import {HoverTip, mountOnBody} from './overlay';

type FileInfo = {id?: string; name?: string};

type Draft = {
    message: string;
    channelId: string;
    rootId?: string;
    fileInfos?: readonly FileInfo[];
    uploadsInProgress?: readonly unknown[];
};

const draftFiles = (draft: Draft): {id: string; name: string}[] => {
    const out: {id: string; name: string}[] = [];
    for (const info of draft.fileInfos ?? []) {
        const id = info?.id ?? '';
        if (!id) {
            continue;
        }
        out.push({id, name: info.name || id});
    }
    return out;
};

// ponytail: Mattermost updateText only replaces message and spreads the same draft
// object, so files are cleared by mutating that object first.
const clearComposer = (draft: Draft, updateText: (message: string) => void) => {
    try {
        const mutable = draft as unknown as {fileInfos: FileInfo[]; uploadsInProgress: unknown[]};
        mutable.fileInfos = [];
        mutable.uploadsInProgress = [];
    } catch {
        // frozen draft: the text still clears, files stay in the composer
    }
    updateText('');
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
    const menuRef = useRef<HTMLDivElement>(null);
    const buttonRef = useRef<HTMLButtonElement>(null);

    useEffect(() => {
        if (!open) {
            return undefined;
        }
        const onDoc = (event: MouseEvent) => {
            const target = event.target as Node;
            if (rootRef.current?.contains(target) || menuRef.current?.contains(target)) {
                return;
            }
            setOpen(false);
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

    const files = draftFiles(draft);
    const uploading = (draft.uploadsInProgress?.length ?? 0) > 0;
    const canSchedule = !uploading && (draft.message.trim() !== '' || files.length > 0);
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
        if (!canSchedule) {
            return;
        }
        handComposerDraft(() => clearComposer(draft, updateText));
        dispatch(openScheduleModal(draft.message, {
            fileIds: files.map((file) => file.id),
            fileNames: files.map((file) => file.name),
            rootId: draft.rootId ?? '',
            channelId: draft.channelId,
        }));
        setOpen(false);
    };

    const onOnline = async () => {
        if (!isDirect || !canSchedule || busyRef.current) {
            return;
        }
        busyRef.current = true;
        setError(null);
        try {
            await createWhenOnline(draft.channelId, draft.message, {
                fileIds: files.map((file) => file.id),
                rootId: draft.rootId ?? '',
            });
            clearComposer(draft, updateText);
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
            <HoverTip
                label='Отложить сообщение'
                suppressed={open}
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
            </HoverTip>
            {open && anchor && mountOnBody(
                <div
                    ref={menuRef}
                    role='menu'
                    style={menuStyle}
                >
                    {uploading && <div style={warnStyle}>{'Дождитесь загрузки файлов.'}</div>}
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
                </div>,
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
