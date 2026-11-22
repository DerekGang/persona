import { describe, it, expect } from '@ohos/hypium';
import { util } from '@kit.ArkTS';
import { calendarManager } from '@kit.CalendarKit';
import { reminderAgentManager } from '@kit.BackgroundTasksKit';
import { nativeTestContext } from './NativeContext';
import { fixtureArgument } from './NativeTransport.test';
import { HarmonyMeetingCalendar } from '../../../main/ets/platform/NativeMeetingServices';
import { MeetingReminderQueue, MeetingReminderSystem, MeetingOccurrence, PublishedMeetingReminder } from '../../../main/ets/models/MeetingCalendarReminders';

export default function nativeMeetingServicesTests():void {
  describe('NativeMeetingServices',():void => {
    it('stores_schedules_updates_and_cancels_phone_calendar_reminders',0,async ():Promise<void> => {
      if (fixtureArgument('meetingReminderFixture')!=='synthetic') throw new Error('isolated_fixture_required');
      const context=nativeTestContext(), name='Persona synthetic meeting '+util.generateRandomUUID(), manager=calendarManager.getCalendarManager(context);
      const calendar=await manager.createCalendar({name:name,type:calendarManager.CalendarType.LOCAL});
      const platform=new HarmonyMeetingCalendar(context), queue=new MeetingReminderQueue(), now=Date.now();
      const start=Math.ceil((now+20*60000)/60000)*60000, end=start+1800000;
      const event:calendarManager.Event={type:calendarManager.EventType.NORMAL,title:name,startTime:start,endTime:end};
      const prefix=String(calendar.id)+'/';
      let allow=true;
      // Reads and mutations below belong only to the test's generated calendar and its own reminder.
      const system:MeetingReminderSystem={
        calendar:async ():Promise<MeetingOccurrence[]> => (await platform.occurrences(now,now+36*3600000,calendar.id)),
        existing:async ():Promise<PublishedMeetingReminder[]> => (await platform.existing()).filter((r:PublishedMeetingReminder) => r.key.startsWith('["'+prefix)),
        publish:(item:MeetingOccurrence)=>platform.publish(item),cancel:(id:number)=>platform.cancel(id)
      };
      try {
        const id=await calendar.addEvent(event); const plan=await system.calendar();
        expect(plan.length).assertEqual(1);expect(plan[0].id).assertEqual(prefix+String(id));
        await queue.refresh(system,()=>allow,()=>'',()=>now);
        const first=await system.existing();expect(first.length).assertEqual(1);
        const sys=await reminderAgentManager.getAllValidReminders(), initial=sys.find((r:reminderAgentManager.ReminderInfo) => r.reminderId===first[0].systemID);
        if (!initial) throw new Error('reminder_not_published');
        const stamp=(initial.reminderReq as reminderAgentManager.ReminderRequestCalendar).dateTime;
        expect(new Date(stamp.year,stamp.month-1,stamp.day,stamp.hour,stamp.minute,stamp.second || 0).getTime()).assertEqual(start-60000);
        expect(initial.reminderReq.wantAgent?.parameters?.['occurrence']).assertEqual(plan[0].key());
        event.id=id;event.title=name+' moved';event.startTime=start+300000;event.endTime=end+300000;await calendar.updateEvent(event);
        await queue.refresh(system,()=>allow,()=>'',()=>now);
        const moved=await system.existing();expect(moved.length).assertEqual(1);expect(moved[0].title).assertEqual(event.title);
        expect(moved[0].key===first[0].key).assertFalse();
        allow=false;await queue.refresh(system,()=>allow,()=>'',()=>now);expect((await system.existing()).length).assertEqual(0);
      } finally {
        try { for (const item of await system.existing()) await platform.cancel(item.systemID); }
        finally { await manager.deleteCalendar(calendar); }
      }
    });
  });
}
