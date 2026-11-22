import { ClientModel } from '../models/ClientModel';
import { MEETING_ROLES, MeetingPreferences, meetingRoleLabel } from '../models/MeetingPreferences';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { PhoneSectionTitle, PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';

@ComponentV2
export struct PhoneMeetingSettings {
  @Require @Param model:ClientModel;
  @Local preferences:MeetingPreferences=new MeetingPreferences();
  aboutToAppear():void { this.preferences.open(this.model).catch(() => {}); }
  aboutToDisappear():void { this.preferences.close(); }
  @Monitor('model.profile','model.connected','model.features') ownerChanged():void { this.preferences.open(this.model).catch(() => {}); }

  build() {
    Scroll() { Column({space:6}) {
      PhoneSectionTitle({text:tr('phone_meeting_role')})
      if (this.preferences.ready) {
        Column() {
          ForEach(MEETING_ROLES,(role:string,i:number) => {
            Button({type:ButtonType.Normal}) {
              Row({space:8}) {
                Text(tr(meetingRoleLabel(role))).fontSize(17).fontColor(Colors.ink).layoutWeight(1)
                if (this.preferences.role===role) SymbolGlyph(Symbols.check).fontSize('17vp').fontColor([Colors.link])
              }.width('100%')
            }.width('100%').constraintSize({minHeight:48}).padding({left:16,right:16,top:7,bottom:7}).backgroundColor(Color.Transparent)
              .enabled(!this.preferences.saving).accessibilitySelected(this.preferences.role===role)
              .onClick(() => this.preferences.choose(role)).id('settings.meetings.role.'+role)
            if (i<MEETING_ROLES.length-1) Divider().strokeWidth(0.5).color(Colors.divider).margin({left:16})
          },(role:string) => role)
        }.width('100%').backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
        Text(tr('phone_meeting_default_note')).fontSize(13).fontColor(Colors.secondary).width('100%').padding({left:12,right:12})
      } else if (!this.model.connected || !this.model.features.includes('meeting_attendance') || this.preferences.error) {
        Text(tr(!this.model.connected ? 'offline' : this.preferences.error || 'phone_meeting_unavailable')).fontSize(15).fontColor(Colors.secondary)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Colors.link).onClick(() => this.preferences.open(this.model))
      } else LoadingProgress().width(26).height(26)
      if (this.preferences.ready && this.preferences.error) Text(tr(this.preferences.error)).fontSize(14).fontColor(Colors.danger)
    }.width('100%').alignItems(HorizontalAlign.Start).padding(16) }.width('100%').height('100%').align(Alignment.TopStart)
      .backgroundColor(Colors.grouped).id('settings.meetings.options')
  }
}
