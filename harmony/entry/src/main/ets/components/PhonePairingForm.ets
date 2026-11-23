import { Colors } from '../design/Colors';
import { PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { isPairingAddress } from '../core/WireCompatibility';

@ComponentV2
export struct PhonePairingForm {
  @Param password: boolean = false;
  @Param manual: boolean = false;
  @Param address: string = '';
  @Param code: string = '';
  @Param error: string = '';
  @Param busy: boolean = false;
  @Param accessExpanded: boolean = false;
  @Param accessID: string = '';
  @Param accessSecret: string = '';
  @Param sshLabel: string = '';
  @Event onAddress: (value: string) => void = () => {};
  @Event onCode: (value: string) => void = () => {};
  @Event onPassword: (value: boolean) => void = () => {};
  @Event onManual: () => void = () => {};
  @Event onPhoto: () => void = () => {};
  @Event onAccessExpanded: (expanded: boolean) => void = () => {};
  @Event onAccessID: (value: string) => void = () => {};
  @Event onAccessSecret: (value: string) => void = () => {};
  @Event onSsh: () => void = () => {};
  @Event onClose: () => void = () => {};
  @Event onConnect: () => void = () => {};

  private typeMetric(regular: number, large: number): number {
    return PhoneStyle.formSize(regular, large, nativeEnvironment.fontScale);
  }

  private tint(): ResourceColor {
    return nativeEnvironment.dark ? '#0A84FF' : '#007AFF';
  }

  private ready(): boolean {
    return !this.busy && !!this.address.trim() &&
      (this.password || !!this.code.trim() || isPairingAddress(this.address.trim()));
  }

  @Builder
  header() {
    Stack() {
      Text(tr('phone_pair_title')).fontSize(this.typeMetric(17, 22) + 'vp').fontWeight(600)
        .fontColor(Colors.ink).maxLines(1)
      Row() {
        Button({ type: ButtonType.Circle }) {
          SymbolGlyph($r('sys.symbol.xmark')).fontSize(17).fontColor([Colors.ink])
        }.width(44).height(44).backgroundColor(Colors.surface).accessibilityText(tr('close'))
          .onClick(() => this.onClose()).id('pair.close')
        Blank()
        Button(tr('connect'), { type: ButtonType.Capsule }).height(44).padding({ left: 16, right: 16 })
          .fontSize(this.typeMetric(17, 22) + 'vp').fontWeight(600)
          .fontColor(this.ready() ? Color.White : Colors.secondary)
          .backgroundColor(this.ready() ? this.tint() : Colors.surface).enabled(this.ready())
          .onClick(() => this.onConnect()).id('pair.verify')
      }.width('100%')
    }.width('100%').height(76).padding({ left: 16, right: 16 })
  }

  @Builder
  segment(isPassword: boolean) {
    Button(tr(isPassword ? 'phone_pair_password' : 'phone_pair_token'), { type: ButtonType.Capsule })
      .layoutWeight(1).height(32).padding({ left: 3, right: 3 })
      .fontSize('13vp').fontWeight(this.password === isPassword ? 600 : 400).fontColor(Colors.ink)
      .backgroundColor(this.password === isPassword ? Colors.surface : Color.Transparent)
      .accessibilitySelected(this.password === isPassword)
      .onClick(() => this.onPassword(isPassword)).id(isPassword ? 'pair.mode.password' : 'pair.mode.token')
  }

  @Builder
  method() {
    Column() {
      Row() {
        this.segment(true)
        this.segment(false)
      }.height(32).borderRadius(16).backgroundColor(Colors.control)
    }.width('100%').padding({ left: 20, right: 20, top: 10, bottom: 10 })
      .borderRadius(24).backgroundColor(Colors.surface)
  }

  @Builder
  caption(value: string) {
    Text(value).fontSize(this.typeMetric(13, 17) + 'vp').fontColor(Colors.secondary)
      .width('100%').padding({ left: 20, right: 20 }).textAlign(TextAlign.Start)
  }

  @Builder
  actionRow(title: string, symbol: Resource, id: string, action: () => void) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 12 }) {
        SymbolGlyph(symbol).fontSize(this.typeMetric(21, 26) + 'vp').fontColor([this.tint()]).accessibilityLevel('no')
        Text(title).fontSize(this.typeMetric(17, 22) + 'vp').fontColor(this.tint()).maxLines(2).layoutWeight(1)
      }.width('100%')
    }.width('100%').constraintSize({ minHeight: this.typeMetric(52, 60) }).padding({ left: 16, right: 16 })
      .borderRadius(20).backgroundColor(Colors.surface).enabled(!this.busy)
      .accessibilityText(title).onClick(action).id(id)
  }

  @Builder
  settingsRow(title: string, symbol: Resource, key: string, trailing: string, expanded: boolean, tap: () => void) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 12 }) {
        SymbolGlyph(symbol).fontSize(this.typeMetric(21, 26) + 'vp').fontColor([this.tint()]).accessibilityLevel('no')
        Text(title).fontSize(this.typeMetric(17, 22) + 'vp').fontColor(Colors.ink).maxLines(1).layoutWeight(1)
        if (trailing) {
          Text(trailing).fontSize(this.typeMetric(15, 19) + 'vp').fontColor(Colors.secondary)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).constraintSize({ maxWidth: '33%' })
        }
        SymbolGlyph(expanded ? $r('sys.symbol.chevron_down') : $r('sys.symbol.chevron_right')).fontSize(12)
          .fontColor([Colors.secondary]).accessibilityLevel('no')
      }.width('100%')
    }.height(this.typeMetric(52, 59.3)).width('100%').padding({ left: 16, right: 16 }).backgroundColor(Color.Transparent)
      .accessibilityText(trailing ? title + ' ' + trailing : title).onClick(tap).id(key)
  }

  @Builder
  accessSection() {
    Column({ space: 6 }) {
      Column() {
        this.settingsRow(tr('phone_pair_access'), $r('sys.symbol.lock'), 'pair.access', '', this.accessExpanded,
          () => this.onAccessExpanded(!this.accessExpanded))
        if (this.accessExpanded) {
          Divider().color($r('app.color.persona_divider')).margin({ left: 20, right: 20 })
          TextInput({ text: this.accessID, placeholder: tr('phone_pair_access_id') })
            .fontSize(this.typeMetric(17, 22) + 'vp').height(this.typeMetric(52, 60)).padding({ left: 20, right: 20 })
            .backgroundColor(Color.Transparent).onChange((value: string) => this.onAccessID(value)).id('pair.access.id')
          Divider().color($r('app.color.persona_divider')).margin({ left: 20, right: 20 })
          TextInput({ text: this.accessSecret, placeholder: tr('phone_pair_access_secret') })
            .type(InputType.Password).showPasswordIcon(false).fontSize(this.typeMetric(17, 22) + 'vp')
            .height(this.typeMetric(52, 60)).padding({ left: 20, right: 20 }).backgroundColor(Color.Transparent)
            .onChange((value: string) => this.onAccessSecret(value)).id('pair.access.secret')
        }
      }.width('100%').borderRadius(20).clip(true).backgroundColor(Colors.surface)
      this.caption(tr('phone_pair_access_info'))
    }
  }

  @Builder
  sshSection() {
    Column({ space: 6 }) {
      Column() {
        this.settingsRow(tr('phone_pair_ssh'), $r('sys.symbol.key'), 'pair.ssh', this.sshLabel, false, () => this.onSsh())
      }.width('100%').borderRadius(20).clip(true).backgroundColor(Colors.surface)
      this.caption(tr(this.sshLabel ? 'phone_pair_ssh_configured' : 'phone_pair_ssh_info'))
    }
  }

  @Builder
  addressFields() {
    Column({ space: 6 }) {
      this.caption(tr(this.password ? 'phone_pair_password' : 'phone_pair_manual'))
      Column() {
        TextInput({ text: this.address, placeholder: tr(this.password ? 'phone_pair_https' : 'phone_pair_address') })
          .type(InputType.URL).fontSize(this.typeMetric(17, 22) + 'vp').height(this.typeMetric(52, 60)).padding(0)
          .backgroundColor(Color.Transparent).onChange((value: string) => this.onAddress(value)).id('pair.address')
        if (!this.password) {
          Divider().color($r('app.color.persona_divider'))
          TextInput({ text: this.code, placeholder: tr('phone_pair_code') })
            .type(InputType.Password).showPasswordIcon(false).fontSize(this.typeMetric(17, 22) + 'vp')
            .height(this.typeMetric(52, 60)).padding(0).backgroundColor(Color.Transparent)
            .onChange((value: string) => this.onCode(value)).id('pair.code')
        }
      }.padding({ left: 20, right: 20 }).width('100%').borderRadius(20).backgroundColor(Colors.surface)
      if (this.password) { this.caption(tr('phone_pair_password_info')) }
    }.width('100%')
  }

  build() {
    Column() {
      this.header()
      Scroll() {
        Column({ space: 28 }) {
          this.method()
          if (this.password || this.manual) {
            this.addressFields()
            if (!this.password) {
              this.sshSection()
              Column({ space: 6 }) {
                this.caption(tr('phone_pair_command'))
                Text('personad hub pair').fontSize(this.typeMetric(15, 19) + 'vp').fontFamily('monospace')
                  .textSelectable(TextSelectableMode.SELECTABLE_UNFOCUSABLE).fontColor(Colors.ink)
                  .padding({ left: 20, right: 20 }).width('100%').height(this.typeMetric(52, 60))
                  .borderRadius(20).backgroundColor(Colors.surface).id('pair.command')
                this.caption(tr('phone_pair_command_info'))
              }
            }
          } else {
            this.actionRow(tr('phone_pair_photo'), $r('sys.symbol.picture'), 'pair.choosePhoto', () => this.onPhoto())
            Column({ space: 6 }) {
              this.actionRow(tr('welcome_action_other'), $r('sys.symbol.ellipsis_circle'), 'pair.other', () => this.onManual())
              this.caption(tr('phone_pair_other_info'))
            }
          }
          if (!this.password) { this.accessSection() }
          if (this.error) {
            Text(this.error).fontSize(this.typeMetric(13, 17) + 'vp').fontColor(Colors.danger).width('100%')
          }
        }.width('100%').padding({ left: 16, right: 16, top: 34, bottom: 32 })
      }.width('100%').layoutWeight(1).align(Alignment.Top).scrollBar(BarState.Auto)
    }.width('100%').height('100%').backgroundColor(Colors.grouped)
  }
}
