export namespace core {
	
	export class AntiDetectConfig {
	    enable_trace_headers: boolean;
	    refresh_fingerprint_per_account: boolean;
	    enable_ip_precheck: boolean;
	    enable_clash_rotation: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AntiDetectConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enable_trace_headers = source["enable_trace_headers"];
	        this.refresh_fingerprint_per_account = source["refresh_fingerprint_per_account"];
	        this.enable_ip_precheck = source["enable_ip_precheck"];
	        this.enable_clash_rotation = source["enable_clash_rotation"];
	    }
	}
	export class ClashConfig {
	    enable: boolean;
	    fastest_mode: boolean;
	    api_url: string;
	    secret: string;
	    group_name: string;
	    mixed_port: number;
	    blacklist: string[];
	    test_proxy_url: string;
	
	    static createFrom(source: any = {}) {
	        return new ClashConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enable = source["enable"];
	        this.fastest_mode = source["fastest_mode"];
	        this.api_url = source["api_url"];
	        this.secret = source["secret"];
	        this.group_name = source["group_name"];
	        this.mixed_port = source["mixed_port"];
	        this.blacklist = source["blacklist"];
	        this.test_proxy_url = source["test_proxy_url"];
	    }
	}

}

export namespace email {
	
	export class CFTempEmailConfig {
	    name: string;
	    url: string;
	    adminAuth: string;
	    domains: string[];
	
	    static createFrom(source: any = {}) {
	        return new CFTempEmailConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.adminAuth = source["adminAuth"];
	        this.domains = source["domains"];
	    }
	}
	export class CloudMailConfig {
	    name: string;
	    url: string;
	    email: string;
	    password: string;
	    domains: string[];
	
	    static createFrom(source: any = {}) {
	        return new CloudMailConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.email = source["email"];
	        this.password = source["password"];
	        this.domains = source["domains"];
	    }
	}
	export class MoeMailConfig {
	    name: string;
	    url: string;
	    apiKey: string;
	
	    static createFrom(source: any = {}) {
	        return new MoeMailConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.apiKey = source["apiKey"];
	    }
	}

}

export namespace proxy {
	
	export class Info {
	    ok: boolean;
	    scheme: string;
	    ip: string;
	    country: string;
	    region: string;
	    city: string;
	    isp: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.scheme = source["scheme"];
	        this.ip = source["ip"];
	        this.country = source["country"];
	        this.region = source["region"];
	        this.city = source["city"];
	        this.isp = source["isp"];
	        this.error = source["error"];
	    }
	}
	export class PoolEntry {
	    id: string;
	    name: string;
	    url: string;
	    weight: number;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PoolEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.url = source["url"];
	        this.weight = source["weight"];
	        this.enabled = source["enabled"];
	    }
	}

}

export namespace task {
	
	export class StartTaskRequest {
	    count: number;
	    concurrency: number;
	    delay: number;
	    outputPath: string;
	    emailProvider: string;
	    moemailDomains: string[];
	    moemailConfigs: Record<string, Array<email.MoeMailConfig>>;
	    moemailRandomMode: boolean;
	    cloudmailDomains: string[];
	    cloudmailConfigs: Record<string, Array<email.CloudMailConfig>>;
	    cloudmailRandomMode: boolean;
	    cftempemailDomains: string[];
	    cftempemailConfigs: Record<string, Array<email.CFTempEmailConfig>>;
	    cftempemailRandomMode: boolean;
	    clash_config?: core.ClashConfig;
	    anti_detect?: core.AntiDetectConfig;
	
	    static createFrom(source: any = {}) {
	        return new StartTaskRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.count = source["count"];
	        this.concurrency = source["concurrency"];
	        this.delay = source["delay"];
	        this.outputPath = source["outputPath"];
	        this.emailProvider = source["emailProvider"];
	        this.moemailDomains = source["moemailDomains"];
	        this.moemailConfigs = this.convertValues(source["moemailConfigs"], Array<email.MoeMailConfig>, true);
	        this.moemailRandomMode = source["moemailRandomMode"];
	        this.cloudmailDomains = source["cloudmailDomains"];
	        this.cloudmailConfigs = this.convertValues(source["cloudmailConfigs"], Array<email.CloudMailConfig>, true);
	        this.cloudmailRandomMode = source["cloudmailRandomMode"];
	        this.cftempemailDomains = source["cftempemailDomains"];
	        this.cftempemailConfigs = this.convertValues(source["cftempemailConfigs"], Array<email.CFTempEmailConfig>, true);
	        this.cftempemailRandomMode = source["cftempemailRandomMode"];
	        this.clash_config = this.convertValues(source["clash_config"], core.ClashConfig);
	        this.anti_detect = this.convertValues(source["anti_detect"], core.AntiDetectConfig);
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

