import {useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent} from 'react'
import * as api from './api'
import {ROOT, type AppInfo, type Bounds, type SessionInfo, type TreeNode} from './api'
import {Icon} from './icons'
import {Tree} from './Tree'
import {SettingsPane, type NewNode} from './SettingsPane'

type Status = 'connecting' | 'connected' | 'disconnected'

interface SessionTab {
    kind: 'session'
    id: string
    sessionId: string
    title: string
    // nodeId is empty for a quick connection; target is what was typed.
    nodeId: string
    target: string
    status: Status
    message: string
}

interface SettingsTab {
    kind: 'settings'
    id: string
    nodeId: string
    // newNode is set while the tab is for a node not yet saved.
    newNode?: NewNode
}

interface AppTab {
    kind: 'app'
    id: 'app'
}

type Tab = SessionTab | SettingsTab | AppTab

interface MenuItem {
    label: string
    action: () => void
    disabled?: boolean
    separator?: boolean
}

interface Menu {
    x: number
    y: number
    items: MenuItem[]
    // Menus that can extend over the session area hide the session while open.
    overSession: boolean
}

interface Message {
    time: string
    source: string
    text: string
}

const findNode = (nodes: TreeNode[], id: string): TreeNode | undefined => {
    for (const n of nodes) {
        if (n.id === id) return n
        const hit = findNode(n.children, id)
        if (hit) return hit
    }
}

const subtreeIds = (n: TreeNode): string[] => [n.id, ...n.children.flatMap(subtreeIds)]

const loadExpanded = (): Set<string> => {
    try {
        return new Set(JSON.parse(localStorage.getItem('expanded') ?? '[]'))
    } catch {
        return new Set()
    }
}

let tabSeq = 0

export default function App() {
    const [info, setInfo] = useState<AppInfo | null>(null)
    const [tree, setTree] = useState<TreeNode[]>([])
    const [version, setVersion] = useState(0)
    const [tabs, setTabs] = useState<Tab[]>([])
    const [activeId, setActiveId] = useState('')
    const [selectedId, setSelectedId] = useState('')
    const [expanded, setExpanded] = useState(loadExpanded)
    const [filter, setFilter] = useState('')
    const [renamingId, setRenamingId] = useState('')
    const [quick, setQuick] = useState('')
    const [menu, setMenu] = useState<Menu | null>(null)
    const [confirm, setConfirm] = useState<{ text: string, button: string, action: () => void } | null>(null)
    const [dirty, setDirty] = useState<Record<string, boolean>>({})
    const [messages, setMessages] = useState<Message[]>([])
    const [showMessages, setShowMessages] = useState(false)
    const [unread, setUnread] = useState(0)
    const [sidebarWidth, setSidebarWidth] = useState(280)
    const [sidebarHidden, setSidebarHidden] = useState(false)
    const [, setLayoutTick] = useState(0)
    const contentRef = useRef<HTMLDivElement>(null)
    const sentBounds = useRef(new Map<string, string>())
    const tabsRef = useRef<Tab[]>([])
    tabsRef.current = tabs

    const log = useCallback((source: string, text: string) => {
        setMessages(m => [...m.slice(-199), {time: new Date().toLocaleTimeString(), source, text}])
        setUnread(n => n + 1)
    }, [])
    const fail = useCallback((e: unknown) => log('myrdp', api.errorText(e)), [log])

    const reload = useCallback(() => {
        api.getTree().then(setTree).catch(fail)
        setVersion(v => v + 1)
    }, [fail])

    useEffect(() => {
        api.getAppInfo().then(i => {
            setInfo(i)
            setSidebarWidth(i.ui.sidebarWidth || 280)
            setSidebarHidden(i.ui.sidebarHidden)
            if (!i.startupError) reload()
        })
    }, [reload])

    useEffect(() => localStorage.setItem('expanded', JSON.stringify([...expanded])), [expanded])

    // Session events drive each tab's status and the message log.
    useEffect(() => api.onSessionEvent(({id, event}) => {
        const update = (status: Status, message = '') => setTabs(ts => ts.map(t =>
            t.kind === 'session' && t.sessionId === id ? {...t, status, message: message || t.message} : t))
        const tab = tabsRef.current.find(t => t.kind === 'session' && t.sessionId === id)
        const source = tab?.kind === 'session' ? tab.title : 'session'
        switch (event.event) {
            case 'disconnected':
                log(source, event.message || `Disconnected (code ${event.reason ?? 0})`)
                break
            case 'error':
            case 'log':
                log(source, event.message ?? '')
                break
            case 'fatalError':
                log(source, `Fatal error (code ${event.reason ?? 0})`)
                break
            case 'logonError':
                // Negative codes are progress notices, not failures.
                if ((event.reason ?? 0) >= 0) log(source, `Logon error (code ${event.reason})`)
                break
            case 'reconnecting':
                log(source, `Connection lost, reconnecting (attempt ${event.attempt ?? 1})`)
                break
        }
        switch (event.event) {
            case 'connecting':
            case 'reconnecting':
                return update('connecting')
            case 'loginComplete':
            case 'reconnected':
                return update('connected')
            case 'disconnected':
                return update('disconnected', event.message || `Disconnected (code ${event.reason ?? 0})`)
            case 'error':
                return update('disconnected', event.message)
            case 'exited':
                return setTabs(ts => ts.map(t => t.kind === 'session' && t.sessionId === id && t.status !== 'disconnected'
                    ? {...t, status: 'disconnected', message: t.message || 'The session ended.'} : t))
        }
    }), [log])

    // Where the native session window goes: the content area, in physical pixels.
    const bounds = (): Bounds => {
        const r = contentRef.current!.getBoundingClientRect()
        const s = window.devicePixelRatio
        return {x: Math.round(r.left * s), y: Math.round(r.top * s), w: Math.round(r.width * s), h: Math.round(r.height * s)}
    }

    // Keep every native session window in step with the layout. The session
    // sits on top of the page, so it is hidden whenever something in the
    // page needs to draw over that area.
    const occluded = confirm !== null || (menu?.overSession ?? false)
    useEffect(() => {
        if (!contentRef.current) return
        const b = bounds()
        const live = new Set<string>()
        for (const t of tabs) {
            if (t.kind !== 'session') continue
            live.add(t.sessionId)
            const visible = t.id === activeId && t.status !== 'disconnected' && !occluded
            const key = JSON.stringify([b, visible])
            if (sentBounds.current.get(t.sessionId) === key) continue
            sentBounds.current.set(t.sessionId, key)
            api.setSessionBounds(t.sessionId, b, visible)
        }
        for (const id of sentBounds.current.keys()) if (!live.has(id)) sentBounds.current.delete(id)
    })
    useEffect(() => {
        if (!contentRef.current) return
        const tick = () => setLayoutTick(n => n + 1)
        const observer = new ResizeObserver(tick)
        observer.observe(contentRef.current)
        window.addEventListener('resize', tick)
        return () => {
            observer.disconnect()
            window.removeEventListener('resize', tick)
        }
    }, [info])

    const activate = (tab: Tab) => {
        setActiveId(tab.id)
        if (tab.kind === 'session' && tab.status !== 'disconnected') api.sessionCommand(tab.sessionId, 'focus').catch(() => {})
    }

    const addSession = (s: SessionInfo, target: string) => {
        const tab: SessionTab = {
            kind: 'session', id: `t${++tabSeq}`, sessionId: s.id, title: s.title, nodeId: s.nodeId,
            target, status: 'connecting', message: '',
        }
        setTabs(ts => [...ts, tab])
        setActiveId(tab.id)
    }

    const connectNode = (nodeId: string) => api.connect(nodeId, bounds()).then(s => addSession(s, '')).catch(fail)

    const quickConnect = () => {
        const target = quick.trim()
        if (!target) return
        api.quickConnect(target, bounds()).then(s => {
            addSession(s, s.nodeId ? '' : target)
            setQuick('')
        }).catch(fail)
    }

    const reconnect = (tab: SessionTab) => {
        const start = tab.nodeId ? api.connect(tab.nodeId, bounds()) : api.quickConnect(tab.target, bounds())
        start.then(s => setTabs(ts => ts.map(t => t.id === tab.id
            ? {...tab, sessionId: s.id, title: s.title, status: 'connecting', message: ''} : t))).catch(fail)
    }

    // Closing a settings tab with unsaved edits asks first.
    const closeTab = (tab: Tab) => {
        if (!dirty[tab.id]) return forceClose(tab)
        setConfirm({text: `Discard the unsaved changes in "${tabTitle(tab)}"?`, button: 'Discard', action: () => forceClose(tab)})
    }

    const forceClose = (tab: Tab) => {
        if (tab.kind === 'session') api.closeSession(tab.sessionId)
        setDirty(d => ({...d, [tab.id]: false}))
        setTabs(ts => {
            const i = ts.findIndex(t => t.id === tab.id)
            const rest = ts.filter(t => t.id !== tab.id)
            if (tab.id === activeId) setActiveId(rest[Math.min(i, rest.length - 1)]?.id ?? '')
            return rest
        })
    }

    const openSettings = (nodeId: string) => {
        const id = `settings:${nodeId}`
        setTabs(ts => ts.some(t => t.id === id) ? ts : [...ts, {kind: 'settings', id, nodeId}])
        setActiveId(id)
    }

    const openAppSettings = () => {
        setTabs(ts => ts.some(t => t.id === 'app') ? ts : [...ts, {kind: 'app', id: 'app'}])
        setActiveId('app')
    }

    // New folders and connections start as an unsaved settings tab; nothing
    // is added to the tree until Save. There is one such tab per type, so
    // asking again returns to it instead of piling up drafts.
    const create = (type: 'folder' | 'connection', parentId = selectedId) => {
        const id = `new:${type}`
        setTabs(ts => ts.some(t => t.id === id) ? ts
            : [...ts, {kind: 'settings', id, nodeId: '', newNode: {parentId: parentId || ROOT, type}}])
        setActiveId(id)
    }

    const saved = (tab: SettingsTab, node: TreeNode) => {
        reload()
        if (!tab.newNode) return
        const parent = findNode(tree, tab.newNode.parentId)
        if (parent?.type === 'folder') setExpanded(e => new Set(e).add(parent.id))
        const id = `settings:${node.id}`
        setTabs(ts => ts.map(t => t.id === tab.id ? {kind: 'settings', id, nodeId: node.id} : t))
        setActiveId(a => a === tab.id ? id : a)
        setSelectedId(node.id)
    }

    const markDirty = useCallback((id: string, value: boolean) =>
        setDirty(d => !!d[id] === value ? d : {...d, [id]: value}), [])

    const remove = (node: TreeNode) => {
        const count = subtreeIds(node).length - 1
        setConfirm({
            text: node.type === 'folder' && count > 0
                ? `Delete the folder "${node.name}" and the ${count} item${count === 1 ? '' : 's'} inside it?`
                : `Delete "${node.name}"?`,
            button: 'Delete',
            action: () => api.deleteNode(node.id).then(() => {
                const gone = new Set(subtreeIds(node))
                setTabs(ts => {
                    const rest = ts.filter(t => !(t.kind === 'settings' && gone.has(t.nodeId)))
                    if (!rest.some(t => t.id === activeId)) setActiveId(rest[rest.length - 1]?.id ?? '')
                    return rest
                })
                setSelectedId('')
                reload()
            }).catch(fail),
        })
    }

    const showMenu = (e: MouseEvent, items: MenuItem[], overSession: boolean) => {
        e.preventDefault()
        setMenu({x: e.clientX, y: e.clientY, items, overSession})
    }

    const treeMenu = (e: MouseEvent, node: TreeNode | null) => {
        const parentId = node ? node.id : ROOT
        const items: MenuItem[] = []
        if (node?.type === 'connection') items.push({label: 'Connect', action: () => connectNode(node.id)})
        if (node) items.push({label: 'Edit settings', action: () => openSettings(node.id), separator: true})
        items.push(
            {label: 'New connection', action: () => create('connection', parentId)},
            {label: 'New folder', action: () => create('folder', parentId), separator: !!node},
        )
        if (node) items.push(
            {label: 'Rename', action: () => setRenamingId(node.id)},
            {label: 'Duplicate', action: () => api.duplicateNode(node.id).then(reload).catch(fail)},
            {label: 'Delete', action: () => remove(node)},
        )
        showMenu(e, items, false)
    }

    const tabMenu = (e: MouseEvent, tab: Tab) => {
        const items: MenuItem[] = []
        if (tab.kind === 'session') {
            const live = tab.status !== 'disconnected'
            items.push(
                {label: 'Reconnect', action: () => reconnect(tab), disabled: live},
                {label: 'Disconnect', action: () => api.sessionCommand(tab.sessionId, 'disconnect').catch(fail), disabled: !live},
                {label: 'Full screen', action: () => api.sessionCommand(tab.sessionId, 'fullscreen').catch(fail), disabled: tab.status !== 'connected'},
                {
                    label: 'Duplicate session', separator: true,
                    action: () => tab.nodeId ? connectNode(tab.nodeId)
                        : api.quickConnect(tab.target, bounds()).then(s => addSession(s, tab.target)).catch(fail),
                },
            )
            if (tab.nodeId) items.push({label: 'Edit settings', action: () => openSettings(tab.nodeId), separator: true})
        }
        items.push({label: 'Close tab', action: () => closeTab(tab)})
        showMenu(e, items, true)
    }

    const saveUI = (width: number, hidden: boolean) => api.saveUI({sidebarWidth: width, sidebarHidden: hidden}).catch(fail)

    const toggleSidebar = useCallback(() => setSidebarHidden(h => {
        api.saveUI({sidebarWidth, sidebarHidden: !h}).catch(() => {})
        return !h
    }), [sidebarWidth])

    const startResize = (e: MouseEvent) => {
        e.preventDefault()
        let width = sidebarWidth
        const move = (ev: globalThis.MouseEvent) => {
            width = Math.max(180, Math.min(600, ev.clientX))
            setSidebarWidth(width)
        }
        const up = () => {
            window.removeEventListener('mousemove', move)
            window.removeEventListener('mouseup', up)
            saveUI(width, false)
        }
        window.addEventListener('mousemove', move)
        window.addEventListener('mouseup', up)
    }

    const selected = useMemo(() => findNode(tree, selectedId), [tree, selectedId])

    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                setMenu(null)
                setConfirm(null)
            }
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [])

    // The native menu bar reports which item was chosen.
    const menuAction = useRef<(action: string) => void>(() => {})
    menuAction.current = action => {
        switch (action) {
            case 'new-connection':
                return create('connection')
            case 'new-folder':
                return create('folder')
            case 'toggle-sidebar':
                return toggleSidebar()
            case 'toggle-messages':
                setUnread(0)
                return setShowMessages(s => !s)
            case 'app-settings':
                return openAppSettings()
            case 'default-settings':
                return openSettings(ROOT)
        }
    }
    useEffect(() => api.onMenu(action => menuAction.current(action)), [])

    const treeKey = (e: ReactKeyboardEvent) => {
        if (!selected || renamingId) return
        if (e.key === 'F2') setRenamingId(selected.id)
        if (e.key === 'Delete') remove(selected)
        if (e.key === 'Enter') selected.type === 'connection' ? connectNode(selected.id) : setExpanded(toggle(selected.id))
    }

    const toggle = (id: string) => (e: Set<string>) => {
        const next = new Set(e)
        next.has(id) ? next.delete(id) : next.add(id)
        return next
    }

    const openIds = useMemo(() => new Set(tabs.flatMap(t =>
        t.kind === 'session' && t.status !== 'disconnected' && t.nodeId ? [t.nodeId] : [])), [tabs])

    const tabTitle = (t: Tab): string => {
        if (t.kind === 'session') return t.title
        if (t.kind === 'app') return 'Settings'
        if (t.newNode) return `New ${t.newNode.type} - Settings`
        const name = t.nodeId === ROOT ? 'Defaults' : findNode(tree, t.nodeId)?.name ?? ''
        return `${name} - Settings`
    }

    // Tell the backend which tabs have unsaved changes, so it can ask before exiting.
    const unsavedNames = tabs.filter(t => dirty[t.id]).map(tabTitle).join('\n')
    useEffect(() => {
        api.setUnsaved(unsavedNames ? unsavedNames.split('\n') : []).catch(() => {})
    }, [unsavedNames])

    if (!info) return null
    if (info.startupError) {
        return <div className="fatal">
            <h1>myrdp could not start</h1>
            <p>{info.startupError}</p>
            <p className="muted">Fix or move the file, then start myrdp again. Nothing has been changed.</p>
        </div>
    }

    const active = tabs.find(t => t.id === activeId)
    const statusText = active?.kind === 'session'
        ? `${active.title}: ${active.status === 'disconnected' ? 'disconnected' : active.status}` : ''

    return <div className="app" onClick={() => menu && setMenu(null)}>
        <header className="topbar">
            <button className="icon-button" title="Show or hide the connection list" onClick={toggleSidebar}>
                <Icon name="sidebar"/></button>
            <button onClick={() => create('connection')}><Icon name="plus"/>Connection</button>
            <button onClick={() => create('folder')}><Icon name="plus"/>Folder</button>
            <form className="quick" onSubmit={e => {
                e.preventDefault()
                quickConnect()
            }}>
                <input value={quick} placeholder="Quick connect: host, host:port or a saved name" spellCheck={false}
                       onChange={e => setQuick(e.target.value)}/>
                <button type="submit" className="primary" disabled={!quick.trim()}>Connect</button>
            </form>
            <button className="icon-button" title="Settings" onClick={openAppSettings}><Icon name="gear"/></button>
        </header>

        <div className="main">
            {!sidebarHidden && <>
                <aside className="sidebar" style={{width: sidebarWidth}} tabIndex={0} onKeyDown={treeKey}>
                    <input className="filter" value={filter} placeholder="Filter connections" spellCheck={false}
                           onChange={e => setFilter(e.target.value)} onKeyDown={e => e.stopPropagation()}/>
                    <Tree nodes={tree} filter={filter} expanded={expanded} selectedId={selectedId} openIds={openIds}
                          renamingId={renamingId}
                          onToggle={id => setExpanded(toggle(id))}
                          onSelect={setSelectedId}
                          onActivate={n => connectNode(n.id)}
                          onContext={treeMenu}
                          onRename={(id, name) => {
                              setRenamingId('')
                              if (name) api.renameNode(id, name).then(reload).catch(fail)
                          }}
                          onMove={(id, parentId, index) => {
                              if (parentId !== ROOT) setExpanded(e => new Set(e).add(parentId))
                              api.moveNode(id, parentId, index).then(reload).catch(fail)
                          }}/>
                </aside>
                <div className="splitter" onMouseDown={startResize}/>
            </>}

            <div className="workspace">
                <div className="tabbar">
                    {tabs.map(t => <div key={t.id} className={'tab' + (t.id === activeId ? ' active' : '')}
                                        title={tabTitle(t)}
                                        onClick={() => activate(t)}
                                        onAuxClick={e => e.button === 1 && closeTab(t)}
                                        onContextMenu={e => tabMenu(e, t)}>
                        {t.kind === 'session' ? <span className={'dot ' + t.status}/> : <Icon name={t.kind === 'app' ? 'gear' : 'edit'}/>}
                        <span className="tab-title">{tabTitle(t)}</span>
                        {dirty[t.id] && <span className="unsaved-dot" title="Unsaved changes"/>}
                        <button className="tab-close" title="Close" onClick={e => {
                            e.stopPropagation()
                            closeTab(t)
                        }}><Icon name="close"/></button>
                    </div>)}
                </div>

                <div className="content" ref={contentRef}>
                    {!active && <div className="pane start">
                        <h1>myrdp</h1>
                        <p className="muted">Double-click a connection to open it, or type a host in the quick connect box.</p>
                    </div>}
                    {active?.kind === 'session' && <div className="pane session">
                        {active.status === 'disconnected'
                            ? <div className="session-card">
                                <h2>{active.title} is disconnected</h2>
                                <p>{active.message}</p>
                                <div className="buttons">
                                    <button className="primary" onClick={() => reconnect(active)}>Reconnect</button>
                                    {active.nodeId && <button onClick={() => openSettings(active.nodeId)}>Edit settings</button>}
                                    <button onClick={() => closeTab(active)}>Close tab</button>
                                </div>
                            </div>
                            : <div className="session-card"><p className="muted">
                                {occluded ? 'The session is still connected.' : `Connecting to ${active.title}…`}</p></div>}
                    </div>}
                    {tabs.map(t => t.kind === 'settings' && <div key={t.id} hidden={t.id !== activeId} className="pane-host">
                        <SettingsPane nodeId={t.nodeId} newNode={t.newNode} version={version}
                                      onSaved={node => saved(t, node)}
                                      onClose={() => forceClose(t)}
                                      onDirty={value => markDirty(t.id, value)}/>
                    </div>)}
                    {active?.kind === 'app' && <div className="pane settings">
                        <div className="settings-scroll"><div className="settings-inner">
                            <h1>Settings</h1>
                            <section>
                                <h2>Connection defaults</h2>
                                <p className="muted">Values every connection starts from, such as a default username or gateway.</p>
                                <button onClick={() => openSettings(ROOT)}>Edit default connection settings</button>
                            </section>
                            <section>
                                <h2>Storage</h2>
                                <label className="radio"><input type="radio" checked readOnly/> Single file</label>
                                <p className="path">{info.connectionsPath}</p>
                                <label className="radio disabled"><input type="radio" disabled/> SQL database (MariaDB), planned for a later version</label>
                                <p className="muted">Passwords are never written to the file. They are stored in the Windows Credential Manager.</p>
                            </section>
                            <section>
                                <h2>Appearance</h2>
                                <p className="muted">myrdp follows the light or dark mode set on your desktop.</p>
                            </section>
                            <section>
                                <h2>About</h2>
                                <p className="muted">myrdp is free software, licensed under the GNU General Public License version 3 or later.</p>
                                <p className="path">Program settings: {info.settingsPath}</p>
                            </section>
                        </div></div>
                    </div>}
                </div>

                {showMessages && <div className="messages">
                    {messages.length === 0 && <div className="muted">No messages.</div>}
                    {messages.slice().reverse().map((m, i) => <div key={i} className="message">
                        <span className="muted">{m.time}</span><b>{m.source}</b><span>{m.text}</span>
                    </div>)}
                </div>}
            </div>
        </div>

        <footer className="statusbar">
            <span>{statusText}</span>
            <button className="link" onClick={() => {
                setShowMessages(s => !s)
                setUnread(0)
            }}>Messages{unread > 0 && !showMessages ? ` (${unread})` : ''}</button>
        </footer>

        {menu && <ContextMenu menu={menu} maxRight={menu.overSession ? window.innerWidth : Math.max(sidebarWidth, 200)}
                              onClose={() => setMenu(null)}/>}
        {confirm && <div className="modal-backdrop" onClick={() => setConfirm(null)}>
            <div className="modal" onClick={e => e.stopPropagation()}>
                <p>{confirm.text}</p>
                <div className="buttons">
                    <button className="danger" autoFocus onClick={() => {
                        confirm.action()
                        setConfirm(null)
                    }}>{confirm.button}</button>
                    <button onClick={() => setConfirm(null)}>Cancel</button>
                </div>
            </div>
        </div>}
    </div>
}

// Tree menus are kept inside the sidebar (maxRight) so they never need to
// draw over a live session.
function ContextMenu({menu, maxRight, onClose}: { menu: Menu, maxRight: number, onClose: () => void }) {
    const ref = useRef<HTMLDivElement>(null)
    const [pos, setPos] = useState({left: menu.x, top: menu.y})
    useEffect(() => {
        const r = ref.current!.getBoundingClientRect()
        setPos({
            left: Math.max(4, Math.min(menu.x, maxRight - r.width - 4)),
            top: Math.max(4, Math.min(menu.y, window.innerHeight - r.height - 4)),
        })
    }, [menu, maxRight])
    return <div ref={ref} className="menu" style={pos} onClick={e => e.stopPropagation()}>
        {menu.items.map(item => <div key={item.label} className={item.separator ? 'menu-group-end' : ''}>
            <button disabled={item.disabled} onClick={() => {
                onClose()
                item.action()
            }}>{item.label}</button>
        </div>)}
    </div>
}
