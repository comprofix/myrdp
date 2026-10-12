const paths: Record<string, string> = {
    folder: 'M1.5 3.5h4l1.5 2h7.5v7h-13z',
    screen: 'M1.5 2.5h13v8h-13zM5 13.5h6M8 10.5v3',
    right: 'M6 4l4 4-4 4',
    down: 'M4 6l4 4 4-4',
    close: 'M4 4l8 8M12 4l-8 8',
    // Cog outline from Lucide's "settings" icon (ISC licence), drawn on a 24-unit grid.
    gear: 'M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2zM12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z',
    sidebar: 'M1.5 2.5h13v11h-13zM5.5 2.5v11',
    plus: 'M8 3v10M3 8h10',
    edit: 'M2.5 13.5l1-3.5 7-7 2.5 2.5-7 7zM9.5 4l2.5 2.5',
}

// Icons are drawn on a 16-unit grid unless listed here.
const gridSize: Record<string, number> = {gear: 24}

export function Icon({name}: { name: string }) {
    const grid = gridSize[name] ?? 16
    return <svg className="icon" viewBox={`0 0 ${grid} ${grid}`} width="16" height="16" fill="none"
                stroke="currentColor" strokeWidth={1.3 * grid / 16} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d={paths[name]}/>
    </svg>
}
