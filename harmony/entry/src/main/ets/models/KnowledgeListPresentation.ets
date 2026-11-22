import type { KnowledgeWorkspace } from './KnowledgeWorkspace';

export function knowledgeDateLabel(raw:string, now:number, locale:string, justNow:string, minutesAgo:string, yesterday:string):string {
  const at=Date.parse(raw);if (!Number.isFinite(at) || at<=946684800000) return '';
  const seconds=(now-at)/1000, date=new Date(at), current=new Date(now);
  if (seconds<45) return justNow;
  if (seconds<3600) return minutesAgo.replace('{n}',String(Math.max(1,Math.floor(seconds/60))));
  const day=(d:Date):number => Date.UTC(d.getFullYear(),d.getMonth(),d.getDate())/86400000;
  const days=day(current)-day(date);
  if (!days) return date.toLocaleTimeString(locale || undefined,{hour:'numeric',minute:'2-digit'});
  if (days===1) return yesterday;
  if (seconds<6*86400) return date.toLocaleDateString(locale || undefined,{weekday:'long'});
  return date.toLocaleDateString(locale || undefined,{month:'short',day:'numeric'});
}
export function knowledgePriorityTone(priority:string):string { return priority==='P0' ? 'attention' : priority==='P1' ? 'working' : priority==='P2' ? 'blue' : 'secondary'; }
export function knowledgeRelevanceTone(relevance:number):string { return relevance>=70 ? 'attention' : relevance>=40 ? 'working' : 'neutral'; }
export function knowledgeInk(tone:string,dark:boolean):string {
  if (tone==='attention') return dark ? '#0A84FF' : '#007AFF';
  if (tone==='working') return dark ? '#FFB224' : '#E8890C';
  if (tone==='blue') return dark ? '#5B9BFF' : '#2E6FD0';
  return dark ? '#98989F' : '#7C7C80';
}
export function knowledgeWash(tone:string,dark:boolean):string {
  return tone==='neutral' ? dark ? '#0FFFFFFF' : '#0F000000' : '#21'+knowledgeInk(tone,dark).slice(1);
}

/** The phone's root navigation bar borrows actions only while their actual knowledge page is visible. */
@ObservedV2
export class KnowledgeLandingMenu {
  @Trace tags:string[]=[];
  @Trace selected:string='';
  @Trace searching:boolean=false;
  private source:KnowledgeWorkspace|undefined;
  private valid:(()=>boolean)|undefined;
  private openMap:(()=>void)|undefined;
  bind(source:KnowledgeWorkspace,current:()=>boolean,map:()=>void):void {
    this.source=source;this.valid=current;this.openMap=map;
    this.tags=source.tags.slice();this.selected=source.tag;this.searching=!!source.query.trim();
  }
  clear(source:KnowledgeWorkspace):void {
    if (this.source!==source) return;
    this.source=undefined;this.valid=undefined;this.openMap=undefined;this.tags=[];this.selected='';this.searching=false;
  }
  choose(tag:string):void {
    if (this.source && this.valid?.() && !this.source.query.trim() && (!tag || this.source.tags.includes(tag))) this.source.setTag(tag);
  }
  map():void { if (this.source && this.valid?.()) this.openMap?.(); }
}
