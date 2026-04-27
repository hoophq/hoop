import { useState, useEffect } from 'react';
import { useDebouncedValue } from '@mantine/hooks';
import { useNavigate } from 'react-router-dom';
import { spotlight } from '@mantine/spotlight';
import { useCommandPaletteStore } from '@/stores/useCommandPaletteStore';
import { useUserStore } from '@/stores/useUserStore';
import { searchAll } from '@/services/search';
import CommandPaletteRoot from './CommandPaletteRoot';
import MainPage from './MainPage';
import ResourceRolesPage from './ResourceRolesPage';
import ConnectionActionsPage, { ACTION_TYPES } from './ConnectionActionsPage';
import { notifications } from '@mantine/notifications'
import { clojureDispatch } from '@/utils/clojureDispatch';

function ConnectedCommandPalette() {
  const navigate = useNavigate();
  const [query, setQuery] = useState('');
  const [debouncedQuery] = useDebouncedValue(query, 300);
  const { currentPage, context, searchStatus, searchResults, setSearchResults, reset, back, navigateToPage } = useCommandPaletteStore();
  const { user } = useUserStore();

  useEffect(
    () => {
      if (debouncedQuery.length >= 2) {
        setSearchResults('searching', {});
        searchAll(debouncedQuery)
          .then(r => setSearchResults('ready', r.data))
          .catch(() => setSearchResults('error', {}));
      } else {
        setSearchResults('idle', {});
      }
    },
    [debouncedQuery, setSearchResults]
  );

  const handleKeyDown = event => {
    if (event.key === 'Backspace' && query === '' && currentPage !== 'main') {
      event.preventDefault();
      back();
    }
  };

  const handleClose = () => {
    setQuery('');
    reset();
  };

  const handleResourceSelect = (resource) => {
    navigateToPage('resource-roles', { resource });
  };

  const handleConnectionSelect = (connection) => {
    navigateToPage('connection-actions', { connection });
  };

  const handleRunbookSelect = (runbook) => {
    spotlight.close();
    navigate(`/runbooks?runbook=${runbook.name}&repository=${runbook.repository}`);
  };

  const handleNavigate = (path) => {
    spotlight.close();
    navigate(path);
  };

  const handleConnectionAction = (actionType, connection, resource) => {
    spotlight.close();

    switch (actionType) {
      case ACTION_TYPES.WEB_TERMINAL:
        navigate(`/client?role=${connection?.name}`);
        break;

      case ACTION_TYPES.HOOP_CLI:
        const cmd = `hoop connect ${connection?.name}`;
        navigator.clipboard.writeText(cmd).then(() => {
          notifications.show({ message: `Copied: ${cmd}`, color: 'green' });
        });
        break;

      case ACTION_TYPES.NATIVE_CLIENT:
        clojureDispatch('native-client-access->start-flow', connection?.name)
        break;

      case ACTION_TYPES.TEST:
        navigate(`/connections/${connection?.name}/test`);
        break;

      case ACTION_TYPES.CONFIGURE:
        navigate(`/roles/${connection?.name}/configure?from=roles-list`);
        break;

      default:
        break;
    }
  };

  const filteredConnections = currentPage === 'resource-roles'
    ? (searchResults.connections || []).filter(c => c.resource_name === context.resource?.name)
    : [];

  return (
    <CommandPaletteRoot
      query={query}
      onQueryChange={setQuery}
      onClose={handleClose}
      onKeyDown={handleKeyDown}
      currentPage={currentPage}
      context={context}
    >
      {currentPage === 'main' && (
        <MainPage
          query={debouncedQuery}
          searchStatus={searchStatus}
          searchResults={searchResults}
          onResourceSelect={handleResourceSelect}
          onConnectionSelect={handleConnectionSelect}
          onRunbookSelect={handleRunbookSelect}
          onNavigate={handleNavigate}
        />
      )}
      {currentPage === 'resource-roles' && (
        <ResourceRolesPage
          resource={context.resource}
          connections={filteredConnections}
          onSelect={handleConnectionSelect}
        />
      )}
      {currentPage === 'connection-actions' && (
        <ConnectionActionsPage
          connection={context.connection}
          resource={context.resource}
          isAdmin={user?.is_admin}
          onAction={handleConnectionAction}
        />
      )}
    </CommandPaletteRoot>
  );
}

export default ConnectedCommandPalette;
