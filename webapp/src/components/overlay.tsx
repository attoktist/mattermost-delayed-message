import React, {useState} from 'react';

type ReactDOMHost = {
    createPortal: (node: React.ReactNode, container: Element) => React.ReactNode;
};

// The thread pane (RHS) has a CSS transform. position:fixed inside it is
// positioned against that pane, while getBoundingClientRect is viewport-based,
// so a menu opens off-screen. Mount on document.body.
export function mountOnBody(node: React.ReactNode): React.ReactNode {
    const host = (window as unknown as {ReactDOM?: ReactDOMHost}).ReactDOM;
    if (!host || typeof document === 'undefined') {
        return node;
    }
    return host.createPortal(node, document.body);
}

type TipProps = {
    label: string;
    suppressed?: boolean;
    children: React.ReactNode;
};

export const HoverTip: React.FC<TipProps> = ({label, suppressed, children}) => {
    const [box, setBox] = useState<{left: number; top: number} | null>(null);
    const show = (el: HTMLElement) => {
        const rect = el.getBoundingClientRect();
        setBox({left: rect.left + rect.width / 2, top: rect.top});
    };
    return (
        <span
            style={{display: 'inline-flex'}}
            onMouseEnter={(event) => show(event.currentTarget)}
            onMouseLeave={() => setBox(null)}
            onFocusCapture={(event) => show(event.currentTarget)}
            onBlurCapture={() => setBox(null)}
        >
            {children}
            {box && !suppressed && mountOnBody(
                <div
                    role='tooltip'
                    style={{
                        position: 'fixed',
                        left: box.left,
                        top: box.top - 6,
                        transform: 'translate(-50%, -100%)',
                        zIndex: 2000,
                        padding: '4px 8px',
                        borderRadius: 4,
                        background: 'var(--center-channel-color, #3d3c40)',
                        color: 'var(--center-channel-bg, #fff)',
                        fontSize: 12,
                        lineHeight: '16px',
                        whiteSpace: 'nowrap',
                        pointerEvents: 'none',
                    }}
                >
                    {label}
                </div>,
            )}
        </span>
    );
};
