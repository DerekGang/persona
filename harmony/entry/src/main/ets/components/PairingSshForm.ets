import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { PairingSsh } from '../models/PairingSsh';
import { SshConfiguration } from '../models/SshTunnel';

@ComponentV2
export struct PairingSshForm {
  @Require @Param draft: PairingSsh;
  @Event onDone: () => void = () => {};
  @Event onClose: () => void = () => {};
  @Local private host: string = '';
  @Local private port: string = '22';
  @Local private username: string = '';
  @Local private fingerprint: string = '';
  @Local private pin: string = '';
  @Local private privateKeyText: string = '';
  @Local private phrase: string = '';
  @Local private error: string = '';
  private visible: boolean = false;

  aboutToAppear(): void {
    this.visible = true;
    const config = this.draft.configuration;
    this.host = config.sshHost; this.port = String(config.sshPort); this.username = config.username;
    this.fingerprint = config.hostKey; this.pin = this.draft.hubPin;
  }
  aboutToDisappear(): void { this.visible = false; this.privateKeyText = ''; this.phrase = ''; }
  private verify(): void {
    const config = new SshConfiguration();
    config.sshHost = this.host.trim(); config.sshPort = Number(this.port); config.username = this.username.trim();
    config.hostKey = this.fingerprint.trim(); this.error = '';
    this.getUIContext().showAlertDialog({
      title: tr('ssh_verify_host'), message: tr('ssh_verify_message') + '\n\n' + config.sshHost + ':' + config.sshPort + '\n' + config.hostKey,
      primaryButton: { value: tr('cancel'), action: (): void => {} },
      secondaryButton: { value: tr('ssh_verified_enable'), action: (): void => {
        if (!this.visible) return;
        try {
          this.draft.configure(config, this.privateKeyText, this.phrase, this.pin, true);
          this.privateKeyText = ''; this.phrase = ''; this.onDone();
        } catch (error) { this.error = (error as Error).message; }
      }}
    });
  }
  private ready(): boolean {
    return !!this.host.trim() && !!this.username.trim() && /^\d{1,5}$/.test(this.port) && Number(this.port) > 0 && Number(this.port) <= 65535 &&
      /^SHA256:[A-Za-z0-9+/]{43}$/.test(this.fingerprint.trim()) && (!this.pin.trim() || /^[A-Za-z0-9_-]{43}$/.test(this.pin.trim())) &&
      (!!this.privateKeyText || this.draft.configured && !this.phrase);
  }
  @Builder subtitle(text: string) {
    Text(text).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%')
  }
  @Builder field(label: string, value: string, id: string, limit: number, kind: InputType, change: (v: string) => void) {
    TextInput({text:value,placeholder:label}).type(kind).maxLength(limit).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17))
      .height(50).backgroundColor(Colors.surface).onChange(change).accessibilityText(label).id(id)
  }
  build() {
    Column() {
      Row({space:8}) {
        Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(()=>this.onClose()).id('ssh.pair.cancel')
        Text(tr('phone_pair_ssh_title')).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontWeight(600).textAlign(TextAlign.Center).layoutWeight(1)
        Button(tr('save'),{buttonStyle:ButtonStyleMode.TEXTUAL}).enabled(this.ready()).onClick(()=>this.verify()).id('ssh.pair.save')
      }.width('100%').height(56).padding({left:12,right:12})
      Scroll() {
        Column({space:12}) {
          if (this.error) Text(tr(this.error)).fontSize(14).fontColor(Colors.danger)
          this.subtitle(tr('phone_pair_ssh_server'))
          Column() {
            this.field(tr('ssh_host'),this.host,'ssh.host',253,InputType.Normal,(v:string)=>{ this.host=v; })
            this.field(tr('ssh_port'),this.port,'ssh.port',5,InputType.Number,(v:string)=>{ this.port=v; })
            this.field(tr('ssh_username'),this.username,'ssh.user',128,InputType.Normal,(v:string)=>{ this.username=v; })
            this.field(tr('ssh_host_key'),this.fingerprint,'ssh.hostKey',50,InputType.Normal,(v:string)=>{ this.fingerprint=v; })
          }.borderRadius(14).clip(true)
          this.subtitle(tr('phone_pair_ssh_key_help'))
          Column() {
            TextArea({text:this.privateKeyText,placeholder:tr(this.draft.configured ? 'ssh_replace_key' : 'ssh_private_key')})
              .maxLength(32768).height(100).fontSize(14).backgroundColor(Colors.surface).onChange((v:string)=>{this.privateKeyText=v;}).id('ssh.privateKey')
            this.field(tr('ssh_passphrase'),this.phrase,'ssh.passphrase',4096,InputType.Password,(v:string)=>{ this.phrase=v; })
          }.borderRadius(14).clip(true)
          this.subtitle(tr('phone_pair_ssh_tls_help'))
          this.field(tr('phone_pair_ssh_tls_pin'),this.pin,'ssh.hubPin',43,InputType.Normal,(v:string)=>{ this.pin=v; })
          if (this.draft.configured) Button(tr('ssh_disable'),{buttonStyle:ButtonStyleMode.TEXTUAL})
            .fontColor(Colors.danger).onClick(()=>{this.draft.clear();this.onDone();}).id('ssh.pair.remove')
        }.width('100%').alignItems(HorizontalAlign.Start).padding({left:16,right:16,top:16,bottom:32})
      }.width('100%').layoutWeight(1)
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('ssh.pair.form')
  }
}
