import {useEffect, useRef, useState, type DragEvent, type MouseEvent} from 'react'
import {ROOT, type TreeNode} from './api'
import {Icon} from './icons'

interface Props {
    nodes: TreeNode[]
    filter: string
    expanded: Set<string>
    selectedId: string
    openIds: Set<string>
    renamingId: string
    onToggle: (id: string) => void
    onSelect: (id: string) => void
    onActivate: (node: TreeNode) => void
    onContext: (e: MouseEvent, node: TreeNode | null) => void
    onRename: (id: string, name: string | null) => void
    onMove: (id: string, parentId: string, index: number) => void
}

// filterTree keeps nodes whose name or host matches, plus their ancestors.
function filterTree(nodes: TreeNode[], q: string): TreeNode[] {
    const out: TreeNode[] = []
    for (const n of nodes) {
        const children = filterTree(n.children, q)
        const hit = n.name.toLowerCase().includes(q) || (n.host ?? '').toLowerCase().includes(q)
        if (hit || children.length > 0) {
            out.push({...n, children: hit && children.length === 0 ? n.children : children})
        }
    }
    return out
}

function contains(node: TreeNode, id: string): boolean {
    return node.id === id || node.children.some(c => contains(c, id))
}

function find(nodes: TreeNode[], id: string): TreeNode | undefined {
    for (const n of nodes) {
        if (n.id === id) return n
        const hit = find(n.children, id)
        if (hit) return hit
    }
}

function RenameInput({name, onDone}: { name: string, onDone: (name: string | null) => void }) {
    const [value, setValue] = useState(name)
    const ref = useRef<HTMLInputElement>(null)
    useEffect(() => ref.current?.select(), [])
    return <input ref={ref} className="rename" value={value}
                  onChange={e => setValue(e.target.value)}
                  onClick={e => e.stopPropagation()}
                  onBlur={() => onDone(value.trim() && value.trim() !== name ? value.trim() : null)}
                  onKeyDown={e => {
                      e.stopPropagation()
                      if (e.key === 'Enter') e.currentTarget.blur()
                      if (e.key === 'Escape') onDone(null)
                  }}/>
}

export function Tree(p: Props) {
    const q = p.filter.trim().toLowerCase()
    const nodes = q ? filterTree(p.nodes, q) : p.nodes
    const [dropId, setDropId] = useState('')

    const canDrop = (e: DragEvent) => e.dataTransfer.types.includes('application/x-myrdp-node')

    // Dropping on a folder moves into it; dropping on a connection places
    // the node just before that connection.
    const drop = (e: DragEvent, target: TreeNode | null, parentId: string, index: number) => {
        e.preventDefault()
        e.stopPropagation()
        setDropId('')
        const id = e.dataTransfer.getData('application/x-myrdp-node')
        const dragged = find(p.nodes, id)
        if (!dragged || (target && contains(dragged, target.id))) return
        if (!target) p.onMove(id, ROOT, -1)
        else if (target.type === 'folder') p.onMove(id, target.id, -1)
        else p.onMove(id, parentId, index)
    }

    const row = (n: TreeNode, depth: number, parentId: string, index: number) => {
        const open = q ? true : p.expanded.has(n.id)
        const isFolder = n.type === 'folder'
        return <div key={n.id}>
            <div className={'row' + (n.id === p.selectedId ? ' selected' : '') + (n.id === dropId ? ' drop' : '')}
                 style={{paddingLeft: 6 + depth * 16}}
                 title={n.host ? `${n.name} (${n.host})` : n.name}
                 draggable={!q && p.renamingId !== n.id}
                 onDragStart={e => {
                     e.dataTransfer.setData('application/x-myrdp-node', n.id)
                     e.dataTransfer.effectAllowed = 'move'
                 }}
                 onDragOver={e => {
                     if (!canDrop(e)) return
                     e.preventDefault()
                     e.stopPropagation()
                     setDropId(n.id)
                 }}
                 onDragLeave={() => setDropId(d => d === n.id ? '' : d)}
                 onDrop={e => drop(e, n, parentId, index)}
                 onClick={() => p.onSelect(n.id)}
                 onDoubleClick={() => isFolder ? p.onToggle(n.id) : p.onActivate(n)}
                 onContextMenu={e => {
                     e.preventDefault()
                     e.stopPropagation()
                     p.onSelect(n.id)
                     p.onContext(e, n)
                 }}>
                <span className="chevron" onClick={e => {
                    if (!isFolder) return
                    e.stopPropagation()
                    p.onToggle(n.id)
                }}>{isFolder && <Icon name={open ? 'down' : 'right'}/>}</span>
                <Icon name={isFolder ? 'folder' : 'screen'}/>
                {p.renamingId === n.id
                    ? <RenameInput name={n.name} onDone={name => p.onRename(n.id, name)}/>
                    : <span className="name">{n.name}</span>}
                {p.openIds.has(n.id) && <span className="dot connected" title="Session open"/>}
            </div>
            {isFolder && open && n.children.map((c, i) => row(c, depth + 1, n.id, i))}
        </div>
    }

    return <div className="tree"
                onClick={() => p.onSelect('')}
                onContextMenu={e => {
                    e.preventDefault()
                    p.onContext(e, null)
                }}
                onDragOver={e => canDrop(e) && e.preventDefault()}
                onDrop={e => drop(e, null, ROOT, -1)}>
        {nodes.map((n, i) => row(n, 0, ROOT, i))}
        {nodes.length === 0 && <div className="empty">
            {q ? 'No connections match the filter.' : 'No connections yet. Use the + buttons above to add one.'}
        </div>}
    </div>
}
