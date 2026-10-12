import {useCallback, useEffect, useState} from 'react'
import * as api from './api'
import type {FieldValue, NodeSettings, TreeNode} from './api'

export interface NewNode {
    parentId: string
    type: 'folder' | 'connection'
}

interface Props {
    // nodeId is the node being edited; newNode is set instead when the tab
    // is for a node that doesn't exist until Save is pressed.
    nodeId: string
    newNode?: NewNode
    // version changes whenever anything in the tree changes, so inherited
    // values shown here stay current.
    version: number
    onSaved: (node: TreeNode) => void
    onClose: () => void
    onDirty: (dirty: boolean) => void
}

const gatewayCredentialKeys = ['rdp.gateway.username', 'rdp.gateway.domain', 'rdp.gateway.passwordStored']

// Hide settings that have no effect given the others.
function isVisible(key: string, values: Record<string, unknown>): boolean {
    if (key.startsWith('rdp.gateway.') && key !== 'rdp.gateway.usage') {
        if (values['rdp.gateway.usage'] === 'never') return false
        if (gatewayCredentialKeys.includes(key)) return values['rdp.gateway.credentials'] === 'separate'
    }
    if (key === 'rdp.autoResize') return values['rdp.resolution'] === 'fit'
    return true
}

const same = (a: unknown, b: unknown) => String(a ?? '') === String(b ?? '')
const isText = (fv: FieldValue) => fv.field.type === 'string' || fv.field.type === 'int'

// Nothing here is written anywhere until Save: edits are held as a draft of
// values to set, overrides to remove, and passwords to store.
export function SettingsPane({nodeId, newNode, version, onSaved, onClose, onDirty}: Props) {
    const [data, setData] = useState<NodeSettings | null>(null)
    const [name, setName] = useState<string | null>(null)
    const [values, setValues] = useState<Record<string, unknown>>({})
    const [resets, setResets] = useState<string[]>([])
    const [secrets, setSecrets] = useState<Record<string, string>>({})
    const [error, setError] = useState('')
    const [saving, setSaving] = useState(false)

    const load = useCallback(() => {
        const request = newNode ? api.getDraftSettings(newNode.parentId, newNode.type) : api.getSettings(nodeId)
        request.then(setData).catch(e => setError(api.errorText(e)))
    }, [nodeId, newNode])
    useEffect(load, [load, version])

    const nameChanged = data !== null && name !== null && name.trim() !== data.name
    const edited = nameChanged || Object.keys(values).length > 0 || resets.length > 0 || Object.keys(secrets).length > 0
    useEffect(() => onDirty(edited), [edited, onDirty])

    if (!data) return <div className="pane settings">{error && <p className="error">{error}</p>}</div>

    const discard = () => {
        setName(null)
        setValues({})
        setResets([])
        setSecrets({})
        setError('')
    }

    const without = <T, >(record: Record<string, T>, key: string) => {
        const {[key]: _, ...rest} = record
        return rest
    }

    // The field as it will be once saved.
    const view = (fv: FieldValue) => {
        const key = fv.field.key
        const kept = fv.overridden && !resets.includes(key)
        if (fv.field.type === 'secret') {
            const typed = secrets[key] ?? ''
            return {value: kept || fv.inherited === true, overridden: kept || typed !== '', pending: typed !== '' || kept !== fv.overridden}
        }
        if (key in values) {
            // An emptied text box means "inherit", applied on save.
            const cleared = isText(fv) && values[key] === ''
            return {value: values[key], overridden: !cleared, pending: true}
        }
        return {value: kept ? fv.value : fv.inherited, overridden: kept, pending: kept !== fv.overridden}
    }

    const change = (fv: FieldValue, v: unknown) => {
        const key = fv.field.key
        setError('')
        const unchanged = same(v, fv.overridden ? fv.value : fv.inherited)
        setValues(unchanged ? without(values, key) : {...values, [key]: v})
        setResets(resets.filter(k => k !== key))
    }

    const reset = (fv: FieldValue) => {
        const key = fv.field.key
        setError('')
        setValues(without(values, key))
        setSecrets(without(secrets, key))
        if (fv.overridden && !resets.includes(key)) setResets([...resets, key])
    }

    const save = () => {
        const finalName = (name ?? data.name).trim()
        if (!data.isRoot && finalName === '') return setError('Enter a name.')
        const set: Record<string, unknown> = {}
        const reset = [...resets]
        for (const fv of data.fields) {
            const key = fv.field.key
            if (!(key in values)) continue
            if (isText(fv) && values[key] === '') fv.overridden && reset.push(key)
            else set[key] = values[key]
        }
        setSaving(true)
        api.saveNode({
            id: newNode ? '' : nodeId, parentId: newNode?.parentId ?? '', type: newNode?.type ?? '',
            name: nameChanged || newNode ? finalName : '', set, reset, secrets,
        }).then(node => {
            discard()
            onSaved(node)
        }).catch(e => setError(api.errorText(e))).finally(() => setSaving(false))
    }

    const effective: Record<string, unknown> = {}
    data.fields.forEach(f => effective[f.field.key] = view(f).value)
    const groups: string[] = []
    data.fields.forEach(f => groups.includes(f.field.group) || groups.push(f.field.group))

    const control = (fv: FieldValue) => {
        const {field} = fv
        const v = view(fv)
        switch (field.type) {
            case 'bool':
                return <input type="checkbox" checked={v.value === true} onChange={e => change(fv, e.target.checked)}/>
            case 'enum':
                return <select value={String(v.value)} onChange={e => change(fv, e.target.value)}>
                    {field.options?.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                </select>
            case 'secret': {
                const kept = fv.overridden && !resets.includes(field.key)
                const hint = kept ? 'Saved in the keyring. Type to replace.'
                    : fv.inherited === true ? 'Inherited. Type to set a different one.' : 'Not set'
                return <input type="password" value={secrets[field.key] ?? ''} placeholder={hint} autoComplete="new-password"
                              onChange={e => {
                                  setError('')
                                  setSecrets(e.target.value ? {...secrets, [field.key]: e.target.value} : without(secrets, field.key))
                                  setResets(resets.filter(k => k !== field.key))
                              }}/>
            }
            default:
                return <input type={field.type === 'int' ? 'number' : 'text'} spellCheck={false}
                              value={String(v.value ?? '')} placeholder={String(fv.inherited ?? '')}
                              onChange={e => change(fv, e.target.value)}/>
        }
    }

    const status = (fv: FieldValue) => {
        const v = view(fv)
        const unsaved = v.pending && <span className="unsaved-note">unsaved</span>
        if (v.overridden) {
            return <>
                <span className="badge">{data.isRoot ? 'Changed' : 'Set here'}</span>
                {unsaved}
                <button className="link" title={data.isRoot ? 'Go back to the built-in default' : 'Go back to the inherited value'}
                        onClick={() => reset(fv)}>Reset</button>
            </>
        }
        if (fv.field.type === 'secret' && fv.inherited !== true) return unsaved || null
        return <>
            <span className="inherited-from">{fv.source === 'built-in default' ? 'Built-in default' : `Inherited from ${fv.source}`}</span>
            {unsaved}
        </>
    }

    const kind = data.type === 'folder' ? 'folder' : 'connection'

    return <div className="pane settings">
        <div className="settings-scroll">
            <div className="settings-inner">
                {data.isRoot
                    ? <>
                        <h1>Default connection settings</h1>
                        <p className="muted">These apply to every connection unless a folder or the connection itself overrides them.</p>
                    </>
                    : <>
                        <label className="name-field">
                            <span>{kind === 'folder' ? 'Folder name' : 'Connection name'}</span>
                            <input value={name ?? data.name} spellCheck={false} autoFocus={!!newNode}
                                   placeholder={`New ${kind}`}
                                   onChange={e => {
                                       setError('')
                                       setName(e.target.value)
                                   }}/>
                        </label>
                        {newNode && <p className="muted">
                            This {kind} is not created until you press Save.
                            {data.parent ? ` It will go in the folder "${data.parent}".` : ' It will go at the top level.'}</p>}
                        {kind === 'folder' &&
                            <p className="muted">Settings on a folder apply to everything inside it, unless overridden lower down.</p>}
                    </>}

                {groups.map(group => {
                    const fields = data.fields.filter(f => f.field.group === group && isVisible(f.field.key, effective))
                    if (fields.length === 0) return null
                    return <section key={group}>
                        <h2>{group}</h2>
                        {fields.map(fv => <div key={fv.field.key} className={'field' + (view(fv).overridden ? '' : ' inherited')}>
                            <label title={fv.field.help}>{fv.field.label}</label>
                            <div className="control">{control(fv)}</div>
                            <div className="status">{status(fv)}</div>
                        </div>)}
                    </section>
                })}
            </div>
        </div>

        <div className="settings-actions">
            <button className="primary" disabled={saving || !(edited || newNode)} onClick={save}>Save</button>
            <button disabled={saving || !(edited || newNode)} onClick={() => newNode ? onClose() : discard()}>Cancel</button>
            {error ? <span className="error">{error}</span>
                : edited && <span className="muted">Unsaved changes</span>}
        </div>
    </div>
}
