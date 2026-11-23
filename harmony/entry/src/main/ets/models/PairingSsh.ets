import type { Pairing } from '../platform/Pairing';
import type { TransportProxy } from '../platform/HubTransport';
import { SshSecrets } from '../platform/SshCredentialVault';
import { SshConfiguration, SshProxyLease, SshTunnel } from './SshTunnel';
import type { ClientViewLease } from './ClientModel';

export interface PairingSshRoute {
  open: (address: string, pin: string) => Promise<TransportProxy>;
  install: (hub: string, address: string, pin: string, view: ClientViewLease, admit: () => boolean) => Promise<(committed: boolean) => Promise<void>>;
  close: () => void;
}

/** A prepared SSH connection belongs to this pairing sheet until the verified Hub redeems a device token. */
@ObservedV2
export class PairingSsh {
  @Trace configured: boolean = false;
  @Trace label: string = '';
  @Trace configuration: SshConfiguration = new SshConfiguration();
  @Trace hubPin: string = '';
  private material: SshSecrets | undefined;
  private request: PairingSshRoute | undefined;

  configure(config: SshConfiguration, privateKey: string, passphrase: string, hubPin: string, confirmed: boolean): void {
    const next = config.copy(); next.sshHost = next.sshHost.trim(); next.username = next.username.trim();
    next.hostKey = next.hostKey.trim(); next.keyRef = ''; next.enabled = true;
    SshTunnel.validateServer(next);
    const pin = hubPin.trim();
    if (!confirmed || (pin && !/^[A-Za-z0-9_-]{43}$/.test(pin))) throw new Error('ssh_invalid_config');
    if (!privateKey && (!this.material || passphrase)) throw new Error('ssh_key_required');
    const replacement = privateKey ? SshSecrets.fromText(privateKey, passphrase) : undefined;
    this.cancelAttempt();
    if (replacement) { this.material?.clear(); this.material = replacement; }
    this.configuration = next; this.hubPin = pin; this.configured = true;
    this.label = next.username + '@' + next.sshHost + (next.sshPort === 22 ? '' : ':' + String(next.sshPort));
  }

  prepare(pairing: Pairing, admit: () => boolean): Pairing {
    this.cancelAttempt();
    const material = this.material;
    if (!this.configured || !material) return pairing;
    const config = this.configuration.copy(), tunnel = new SshTunnel();
    let active = true, endpoint = '', pinned = '', lease: SshProxyLease | undefined;
    const current = (): boolean => active && admit() && this.material === material;
    const route: PairingSshRoute = {
      open: async (address: string, pin: string): Promise<TransportProxy> => {
        if (!current() || (endpoint && (endpoint !== address || pin !== pinned))) throw new Error('ssh_cancelled');
        endpoint = address; pinned = pin;
        if (lease?.isCurrent()) return lease;
        lease = await tunnel.openPairing(address, pin, config, material, current);
        if (!current() || !lease.isCurrent()) throw new Error('ssh_cancelled');
        return lease;
      },
      install: async (hub: string, address: string, pin: string, view: ClientViewLease, owns: () => boolean): Promise<(committed: boolean) => Promise<void>> => {
        const valid = (): boolean => current() && owns() && !!lease?.isCurrent() && endpoint === address && pinned === pin;
        if (!valid()) throw new Error('ssh_cancelled');
        return SshTunnel.stagePairing(hub, config, material, view, valid);
      },
      close: (): void => { active = false; tunnel.stop(); lease = undefined; }
    };
    pairing.setSshRoute(route, this.hubPin); this.request = route;
    return pairing;
  }

  cancelAttempt(): void { this.request?.close(); this.request = undefined; }
  clear(): void {
    this.cancelAttempt(); this.material?.clear(); this.material = undefined;
    this.configuration = new SshConfiguration(); this.hubPin = ''; this.label = ''; this.configured = false;
  }
}
