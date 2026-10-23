import { ConfigStatus } from './ConfigStatus';
import { NAV, PENDING_APPROVALS } from './gatewayNav';
import { Sidebar } from './Sidebar';
import { usePendingApprovalsCount } from './usePendingApprovalsCount';

function GatewaySidebar() {
  const pendingApprovals = usePendingApprovalsCount();
  return <Sidebar nav={NAV} top={<ConfigStatus />} counts={{ [PENDING_APPROVALS]: pendingApprovals }} />;
}

export default GatewaySidebar;
