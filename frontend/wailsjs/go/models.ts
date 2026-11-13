export namespace agent {
	
	export class DiffLine {
	    op: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new DiffLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.op = source["op"];
	        this.text = source["text"];
	    }
	}
	export class Change {
	    path: string;
	    note: string;
	    lines: DiffLine[];
	
	    static createFrom(source: any = {}) {
	        return new Change(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.note = source["note"];
	        this.lines = this.convertValues(source["lines"], DiffLine);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class InputField {
	    label: string;
	    value: string;
	    claim: boolean;
	
	    static createFrom(source: any = {}) {
	        return new InputField(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.value = source["value"];
	        this.claim = source["claim"];
	    }
	}
	export class Item {
	    seq: number;
	    rev: number;
	    kind: string;
	    text: string;
	    tool: string;
	    input: string;
	    change?: Change;
	    result: string;
	    done: boolean;
	    isError: boolean;
	    decision: string;
	    live: boolean;
	    atMs: number;
	
	    static createFrom(source: any = {}) {
	        return new Item(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.rev = source["rev"];
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.tool = source["tool"];
	        this.input = source["input"];
	        this.change = this.convertValues(source["change"], Change);
	        this.result = source["result"];
	        this.done = source["done"];
	        this.isError = source["isError"];
	        this.decision = source["decision"];
	        this.live = source["live"];
	        this.atMs = source["atMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Permission {
	    requestId: string;
	    tool: string;
	    fields: InputField[];
	    change?: Change;
	    hidden: number;
	    marked: number;
	    atMs: number;
	
	    static createFrom(source: any = {}) {
	        return new Permission(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requestId = source["requestId"];
	        this.tool = source["tool"];
	        this.fields = this.convertValues(source["fields"], InputField);
	        this.change = this.convertValues(source["change"], Change);
	        this.hidden = source["hidden"];
	        this.marked = source["marked"];
	        this.atMs = source["atMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class View {
	    id: string;
	    dir: string;
	    title: string;
	    state: string;
	    activity: string;
	    activityTool: string;
	    model: string;
	    mode: string;
	    costUsd: number;
	    turns: number;
	    startedAtMs: number;
	    problem: string;
	    rev: number;
	    firstSeq: number;
	    items: Item[];
	    pending: Permission[];
	
	    static createFrom(source: any = {}) {
	        return new View(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.dir = source["dir"];
	        this.title = source["title"];
	        this.state = source["state"];
	        this.activity = source["activity"];
	        this.activityTool = source["activityTool"];
	        this.model = source["model"];
	        this.mode = source["mode"];
	        this.costUsd = source["costUsd"];
	        this.turns = source["turns"];
	        this.startedAtMs = source["startedAtMs"];
	        this.problem = source["problem"];
	        this.rev = source["rev"];
	        this.firstSeq = source["firstSeq"];
	        this.items = this.convertValues(source["items"], Item);
	        this.pending = this.convertValues(source["pending"], Permission);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class ChatResult {
	    view: agent.View;
	    problem: string;
	
	    static createFrom(source: any = {}) {
	        return new ChatResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.view = this.convertValues(source["view"], agent.View);
	        this.problem = source["problem"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ClaudeInfo {
	    path: string;
	    problem: string;
	
	    static createFrom(source: any = {}) {
	        return new ClaudeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.problem = source["problem"];
	    }
	}
	export class Notice {
	    id: number;
	    atMs: number;
	    title: string;
	    body: string;
	    projectPath: string;
	    sessionId: string;
	    chat: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Notice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.atMs = source["atMs"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.projectPath = source["projectPath"];
	        this.sessionId = source["sessionId"];
	        this.chat = source["chat"];
	    }
	}
	export class StartResult {
	    id: string;
	    problem: string;
	
	    static createFrom(source: any = {}) {
	        return new StartResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.problem = source["problem"];
	    }
	}
	export class SummaryResult {
	    summary: monitor.Summary;
	    problem: string;
	
	    static createFrom(source: any = {}) {
	        return new SummaryResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.summary = this.convertValues(source["summary"], monitor.Summary);
	        this.problem = source["problem"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TrailResult {
	    trail: monitor.Trail;
	    problem: string;
	
	    static createFrom(source: any = {}) {
	        return new TrailResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.trail = this.convertValues(source["trail"], monitor.Trail);
	        this.problem = source["problem"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TrustResult {
	    ok: boolean;
	    problem: string;
	
	    static createFrom(source: any = {}) {
	        return new TrustResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.problem = source["problem"];
	    }
	}

}

export namespace monitor {
	
	export class Activity {
	    kind: string;
	    tool: string;
	    detail: string;
	    atMs: number;
	
	    static createFrom(source: any = {}) {
	        return new Activity(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.tool = source["tool"];
	        this.detail = source["detail"];
	        this.atMs = source["atMs"];
	    }
	}
	export class Limit {
	    kind: string;
	    resetsAtMs: number;
	    atMs: number;
	
	    static createFrom(source: any = {}) {
	        return new Limit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.resetsAtMs = source["resetsAtMs"];
	        this.atMs = source["atMs"];
	    }
	}
	export class CostParts {
	    input: number;
	    output: number;
	    cacheRead: number;
	    cacheWrite: number;
	    webSearch: number;
	
	    static createFrom(source: any = {}) {
	        return new CostParts(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.input = source["input"];
	        this.output = source["output"];
	        this.cacheRead = source["cacheRead"];
	        this.cacheWrite = source["cacheWrite"];
	        this.webSearch = source["webSearch"];
	    }
	}
	export class Spend {
	    tokens: usage.Tokens;
	    totalTokens: number;
	    costUsd: number;
	    costParts: CostParts;
	    unpricedTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new Spend(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tokens = this.convertValues(source["tokens"], usage.Tokens);
	        this.totalTokens = source["totalTokens"];
	        this.costUsd = source["costUsd"];
	        this.costParts = this.convertValues(source["costParts"], CostParts);
	        this.unpricedTokens = source["unpricedTokens"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Agent {
	    id: string;
	    type: string;
	    description: string;
	    model: string;
	    modelLabel: string;
	    status: string;
	    awaitingTool: boolean;
	    workflowId: string;
	    phase: string;
	    background: boolean;
	    startedAtMs: number;
	    lastAtMs: number;
	    durationMs: number;
	    silentMs: number;
	    contextTokens: number;
	    toolUses: number;
	    all: Spend;
	    today: Spend;
	    activity: Activity;
	    pulse: number[];
	    limit?: Limit;
	
	    static createFrom(source: any = {}) {
	        return new Agent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.description = source["description"];
	        this.model = source["model"];
	        this.modelLabel = source["modelLabel"];
	        this.status = source["status"];
	        this.awaitingTool = source["awaitingTool"];
	        this.workflowId = source["workflowId"];
	        this.phase = source["phase"];
	        this.background = source["background"];
	        this.startedAtMs = source["startedAtMs"];
	        this.lastAtMs = source["lastAtMs"];
	        this.durationMs = source["durationMs"];
	        this.silentMs = source["silentMs"];
	        this.contextTokens = source["contextTokens"];
	        this.toolUses = source["toolUses"];
	        this.all = this.convertValues(source["all"], Spend);
	        this.today = this.convertValues(source["today"], Spend);
	        this.activity = this.convertValues(source["activity"], Activity);
	        this.pulse = source["pulse"];
	        this.limit = this.convertValues(source["limit"], Limit);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ClosedSession {
	    id: string;
	    title: string;
	    model: string;
	    modelLabel: string;
	    gitBranch: string;
	    endedAtMs: number;
	    agents: number;
	    all: Spend;
	    today: Spend;
	
	    static createFrom(source: any = {}) {
	        return new ClosedSession(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.model = source["model"];
	        this.modelLabel = source["modelLabel"];
	        this.gitBranch = source["gitBranch"];
	        this.endedAtMs = source["endedAtMs"];
	        this.agents = source["agents"];
	        this.all = this.convertValues(source["all"], Spend);
	        this.today = this.convertValues(source["today"], Spend);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Count {
	    name: string;
	    n: number;
	
	    static createFrom(source: any = {}) {
	        return new Count(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.n = source["n"];
	    }
	}
	export class Counts {
	    projects: number;
	    sessions: number;
	    busySessions: number;
	    closedSessions: number;
	    agents: number;
	    runningAgents: number;
	    quietAgents: number;
	
	    static createFrom(source: any = {}) {
	        return new Counts(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.projects = source["projects"];
	        this.sessions = source["sessions"];
	        this.busySessions = source["busySessions"];
	        this.closedSessions = source["closedSessions"];
	        this.agents = source["agents"];
	        this.runningAgents = source["runningAgents"];
	        this.quietAgents = source["quietAgents"];
	    }
	}
	export class HistoryScan {
	    running: boolean;
	    done: number;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new HistoryScan(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.done = source["done"];
	        this.total = source["total"];
	    }
	}
	export class HistoryPart {
	    key: string;
	    name: string;
	    path: string;
	    costUsd: number;
	    tokens: number;
	    unpricedTokens: number;
	
	    static createFrom(source: any = {}) {
	        return new HistoryPart(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.name = source["name"];
	        this.path = source["path"];
	        this.costUsd = source["costUsd"];
	        this.tokens = source["tokens"];
	        this.unpricedTokens = source["unpricedTokens"];
	    }
	}
	export class HistoryDay {
	    day: string;
	    spend: Spend;
	    sessions: number;
	    projects: HistoryPart[];
	    models: HistoryPart[];
	
	    static createFrom(source: any = {}) {
	        return new HistoryDay(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.day = source["day"];
	        this.spend = this.convertValues(source["spend"], Spend);
	        this.sessions = source["sessions"];
	        this.projects = this.convertValues(source["projects"], HistoryPart);
	        this.models = this.convertValues(source["models"], HistoryPart);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class History {
	    today: string;
	    days: HistoryDay[];
	    firstDay: string;
	    scan: HistoryScan;
	    pricingAsOf: string;
	    kept: boolean;
	
	    static createFrom(source: any = {}) {
	        return new History(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.today = source["today"];
	        this.days = this.convertValues(source["days"], HistoryDay);
	        this.firstDay = source["firstDay"];
	        this.scan = this.convertValues(source["scan"], HistoryScan);
	        this.pricingAsOf = source["pricingAsOf"];
	        this.kept = source["kept"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	
	export class LimitGroup {
	    kind: string;
	    resetsAtMs: number;
	    sessions: number;
	    agents: number;
	
	    static createFrom(source: any = {}) {
	        return new LimitGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.resetsAtMs = source["resetsAtMs"];
	        this.sessions = source["sessions"];
	        this.agents = source["agents"];
	    }
	}
	export class ModelChoice {
	    id: string;
	    label: string;
	    openUsd: number;
	
	    static createFrom(source: any = {}) {
	        return new ModelChoice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.openUsd = source["openUsd"];
	    }
	}
	export class ModelRow {
	    model: string;
	    label: string;
	    priced: boolean;
	    verified: boolean;
	    all: Spend;
	    today: Spend;
	
	    static createFrom(source: any = {}) {
	        return new ModelRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.label = source["label"];
	        this.priced = source["priced"];
	        this.verified = source["verified"];
	        this.all = this.convertValues(source["all"], Spend);
	        this.today = this.convertValues(source["today"], Spend);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Session {
	    id: string;
	    name: string;
	    title: string;
	    pid: number;
	    status: string;
	    waitingFor: string;
	    statusSinceMs: number;
	    entrypoint: string;
	    version: string;
	    gitBranch: string;
	    startedAtMs: number;
	    lastAtMs: number;
	    model: string;
	    modelLabel: string;
	    contextTokens: number;
	    lastPrompt: string;
	    activity: Activity;
	    pulse: number[];
	    own: Spend;
	    all: Spend;
	    today: Spend;
	    agents: Agent[];
	    activeAgents: number;
	    recordedCostUsd: number;
	    hasTranscript: boolean;
	    duplicate: boolean;
	    managed: boolean;
	    awaitingUser: boolean;
	    limit?: Limit;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.title = source["title"];
	        this.pid = source["pid"];
	        this.status = source["status"];
	        this.waitingFor = source["waitingFor"];
	        this.statusSinceMs = source["statusSinceMs"];
	        this.entrypoint = source["entrypoint"];
	        this.version = source["version"];
	        this.gitBranch = source["gitBranch"];
	        this.startedAtMs = source["startedAtMs"];
	        this.lastAtMs = source["lastAtMs"];
	        this.model = source["model"];
	        this.modelLabel = source["modelLabel"];
	        this.contextTokens = source["contextTokens"];
	        this.lastPrompt = source["lastPrompt"];
	        this.activity = this.convertValues(source["activity"], Activity);
	        this.pulse = source["pulse"];
	        this.own = this.convertValues(source["own"], Spend);
	        this.all = this.convertValues(source["all"], Spend);
	        this.today = this.convertValues(source["today"], Spend);
	        this.agents = this.convertValues(source["agents"], Agent);
	        this.activeAgents = source["activeAgents"];
	        this.recordedCostUsd = source["recordedCostUsd"];
	        this.hasTranscript = source["hasTranscript"];
	        this.duplicate = source["duplicate"];
	        this.managed = source["managed"];
	        this.awaitingUser = source["awaitingUser"];
	        this.limit = this.convertValues(source["limit"], Limit);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Project {
	    name: string;
	    path: string;
	    sessions: Session[];
	    closed: ClosedSession[];
	    all: Spend;
	    today: Spend;
	    pinned: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Project(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.sessions = this.convertValues(source["sessions"], Session);
	        this.closed = this.convertValues(source["closed"], ClosedSession);
	        this.all = this.convertValues(source["all"], Spend);
	        this.today = this.convertValues(source["today"], Spend);
	        this.pinned = source["pinned"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Snapshot {
	    generatedAtMs: number;
	    today: string;
	    configDir: string;
	    counts: Counts;
	    all: Spend;
	    todaySpend: Spend;
	    closedAll: Spend;
	    closedToday: Spend;
	    projects: Project[];
	    models: ModelRow[];
	    pricingAsOf: string;
	    pricingSource: string;
	    pricingPath: string;
	    warnings: string[];
	    limits: LimitGroup[];
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.generatedAtMs = source["generatedAtMs"];
	        this.today = source["today"];
	        this.configDir = source["configDir"];
	        this.counts = this.convertValues(source["counts"], Counts);
	        this.all = this.convertValues(source["all"], Spend);
	        this.todaySpend = this.convertValues(source["todaySpend"], Spend);
	        this.closedAll = this.convertValues(source["closedAll"], Spend);
	        this.closedToday = this.convertValues(source["closedToday"], Spend);
	        this.projects = this.convertValues(source["projects"], Project);
	        this.models = this.convertValues(source["models"], ModelRow);
	        this.pricingAsOf = source["pricingAsOf"];
	        this.pricingSource = source["pricingSource"];
	        this.pricingPath = source["pricingPath"];
	        this.warnings = source["warnings"];
	        this.limits = this.convertValues(source["limits"], LimitGroup);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class Summary {
	    sessionId: string;
	    prompts: number;
	    toolCalls: number;
	    tools: Count[];
	    agents: number;
	    workflows: number;
	    filesChanged: number;
	    files: Count[];
	    linesAdded: number;
	    linesRemoved: number;
	    commits: number;
	    pushes: number;
	    firstAtMs: number;
	    lastAtMs: number;
	    toolMs: number;
	    allowed: Count[];
	    denied: Count[];
	    modes: Count[];
	    compactions: number;
	    compactedFrom: number;
	    compactedTo: number;
	    cacheMisses: number;
	    cacheMissTokens: number;
	    responses: number;
	    outputTokens: number;
	    thinkingTokens: number;
	    efforts: Count[];
	    partial: boolean;
	    hidden: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.prompts = source["prompts"];
	        this.toolCalls = source["toolCalls"];
	        this.tools = this.convertValues(source["tools"], Count);
	        this.agents = source["agents"];
	        this.workflows = source["workflows"];
	        this.filesChanged = source["filesChanged"];
	        this.files = this.convertValues(source["files"], Count);
	        this.linesAdded = source["linesAdded"];
	        this.linesRemoved = source["linesRemoved"];
	        this.commits = source["commits"];
	        this.pushes = source["pushes"];
	        this.firstAtMs = source["firstAtMs"];
	        this.lastAtMs = source["lastAtMs"];
	        this.toolMs = source["toolMs"];
	        this.allowed = this.convertValues(source["allowed"], Count);
	        this.denied = this.convertValues(source["denied"], Count);
	        this.modes = this.convertValues(source["modes"], Count);
	        this.compactions = source["compactions"];
	        this.compactedFrom = source["compactedFrom"];
	        this.compactedTo = source["compactedTo"];
	        this.cacheMisses = source["cacheMisses"];
	        this.cacheMissTokens = source["cacheMissTokens"];
	        this.responses = source["responses"];
	        this.outputTokens = source["outputTokens"];
	        this.thinkingTokens = source["thinkingTokens"];
	        this.efforts = this.convertValues(source["efforts"], Count);
	        this.partial = source["partial"];
	        this.hidden = source["hidden"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TrailStep {
	    kind: string;
	    tool: string;
	    detail: string;
	    note: string;
	    result: string;
	    done: boolean;
	    failed: boolean;
	    atMs: number;
	
	    static createFrom(source: any = {}) {
	        return new TrailStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.tool = source["tool"];
	        this.detail = source["detail"];
	        this.note = source["note"];
	        this.result = source["result"];
	        this.done = source["done"];
	        this.failed = source["failed"];
	        this.atMs = source["atMs"];
	    }
	}
	export class Trail {
	    sessionId: string;
	    agentId: string;
	    steps: TrailStep[];
	    earlier: boolean;
	    hidden: boolean;
	    closed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Trail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sessionId = source["sessionId"];
	        this.agentId = source["agentId"];
	        this.steps = this.convertValues(source["steps"], TrailStep);
	        this.earlier = source["earlier"];
	        this.hidden = source["hidden"];
	        this.closed = source["closed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace usage {
	
	export class Tokens {
	    input: number;
	    output: number;
	    cacheRead: number;
	    cacheWrite5m: number;
	    cacheWrite1h: number;
	    webSearches: number;
	    requests: number;
	
	    static createFrom(source: any = {}) {
	        return new Tokens(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.input = source["input"];
	        this.output = source["output"];
	        this.cacheRead = source["cacheRead"];
	        this.cacheWrite5m = source["cacheWrite5m"];
	        this.cacheWrite1h = source["cacheWrite1h"];
	        this.webSearches = source["webSearches"];
	        this.requests = source["requests"];
	    }
	}

}

