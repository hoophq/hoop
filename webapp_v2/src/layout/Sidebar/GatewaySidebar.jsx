import { ConfigStatus } from './ConfigStatus';
import { NAV } from './gatewayNav';
import { Sidebar } from './Sidebar';

function GatewaySidebar() {
  return <Sidebar nav={NAV} top={<ConfigStatus />} />;
}

export default GatewaySidebar;
