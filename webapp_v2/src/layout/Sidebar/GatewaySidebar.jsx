import { ConfigStatus } from './ConfigStatus';
import { NAV, PENDING_APPROVALS } from './gatewayNav';
import { Sidebar } from './Sidebar';

function GatewaySidebar({ pendingApprovals }) {
  return <Sidebar nav={NAV} top={<ConfigStatus />} counts={{ [PENDING_APPROVALS]: pendingApprovals }} />;
}

export default GatewaySidebar;
