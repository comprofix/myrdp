// Typed wrappers around the Go API that Wails generates bindings for.
import * as Go from '../wailsjs/go/main/App'
import {EventsOn} from '../wailsjs/runtime/runtime'

export const ROOT = 'root'

export interface TreeNode {
    id: string
    type: 'folder' | 'connection'
    name: string
    host?: string
    children: TreeNode[]
}

export interface Option {
    value: string
    label: string
}

export interface Field {
    key: string
    label: string
    group: string
    type: 'string' | 'int' | 'bool' | 'enum' | 'secret'
    options?: Option[]
    help?: string
}

export interface FieldValue {
    field: Field
    value: unknown
    overridden: boolean
    // inherited is the value without this node's own override; source says where it comes from.
    inherited: unknown
    source: string
}

export interface NodeSettings {
    id: string
    name: string
    type: 'folder' | 'connection'
    isRoot: boolean
    parent: string
    fields: FieldValue[]
}

export interface SaveRequest {
    id: string
    parentId: string
    type: string
    name: string
    set: Record<string, unknown>
    reset: string[]
    secrets: Record<string, string>
}

export interface Bounds {
    x: number
    y: number
    w: number
    h: number
}

export interface SessionInfo {
    id: string
    title: string
    nodeId: string
}

export interface SessionEvent {
    id: string
    event: { event: string, reason?: number, extendedReason?: number, message?: string, attempt?: number }
}

export interface UISettings {
    sidebarWidth: number
    sidebarHidden: boolean
}

export interface AppInfo {
    connectionsPath: string
    settingsPath: string
    storageType: string
    ui: UISettings
    startupError: string
}

// The generated signatures use generated classes; plain objects are what
// actually cross the bridge, so the wrappers below carry the real types.
const go = Go as any

export const getAppInfo = (): Promise<AppInfo> => go.GetAppInfo()
export const saveUI = (ui: UISettings): Promise<void> => go.SaveUI(ui)
export const setUnsaved = (names: string[]): Promise<void> => go.SetUnsaved(names)
export const getTree = (): Promise<TreeNode[]> => go.GetTree()
export const renameNode = (id: string, name: string): Promise<void> => go.RenameNode(id, name)
export const deleteNode = (id: string): Promise<void> => go.DeleteNode(id)
export const moveNode = (id: string, parentId: string, index: number): Promise<void> => go.MoveNode(id, parentId, index)
export const duplicateNode = (id: string): Promise<TreeNode> => go.DuplicateNode(id)
export const getSettings = (id: string): Promise<NodeSettings> => go.GetSettings(id)
export const getDraftSettings = (parentId: string, type: string): Promise<NodeSettings> => go.GetDraftSettings(parentId, type)
export const saveNode = (req: SaveRequest): Promise<TreeNode> => go.SaveNode(req)
export const connect = (nodeId: string, b: Bounds): Promise<SessionInfo> => go.Connect(nodeId, b)
export const quickConnect = (target: string, b: Bounds): Promise<SessionInfo> => go.QuickConnect(target, b)
export const setSessionBounds = (id: string, b: Bounds, visible: boolean): Promise<void> => go.SetSessionBounds(id, b, visible)
export const sessionCommand = (id: string, cmd: string): Promise<void> => go.SessionCommand(id, cmd)
export const closeSession = (id: string): Promise<void> => go.CloseSession(id)

export const onMenu = (fn: (action: string) => void): (() => void) => EventsOn('menu', fn)
export const onSessionEvent = (fn: (e: SessionEvent) => void): (() => void) => EventsOn('session', fn)

// errorText turns whatever a rejected Go call throws into a message.
export const errorText = (e: unknown): string => typeof e === 'string' ? e : e instanceof Error ? e.message : String(e)
