const ICONS = {
  postgres: '/icons/connections/postgres-default.svg',
  'postgres-csv': '/icons/connections/postgres-default.svg',
  mysql: '/icons/connections/mysql-default.svg',
  'mysql-csv': '/icons/connections/mysql-default.svg',
  mssql: '/icons/connections/mssql-default.svg',
  'sql-server': '/icons/connections/mssql-default.svg',
  'sql-server-csv': '/icons/connections/mssql-default.svg',
  mongodb: '/icons/connections/mongodb-default.svg',
  oracledb: '/icons/connections/oracle-default.svg',
  ssh: '/icons/connections/custom-ssh.svg',
  'command-line': '/icons/connections/custom-ssh.svg',
  custom: '/icons/connections/custom-ssh.svg',
  tcp: '/icons/connections/custom-tcp-http.svg',
  rdp: '/icons/connections/custom-tcp-http.svg',
  httpproxy: '/icons/connections/custom-tcp-http.svg',
  aws: '/icons/connections/aws-default.svg',
  'aws-cli': '/icons/connections/aws-default.svg',
  'aws-ecs': '/icons/connections/aws-default.svg',
  'aws-ssm': '/icons/connections/aws-default.svg',
  awscli: '/icons/connections/awscli-default.svg',
  nodejs: '/icons/connections/node-default.svg',
  python: '/icons/connections/python-default.svg',
  'ruby-on-rails': '/icons/connections/rails-default.svg',
  clojure: '/icons/connections/clojure-default.svg',
  kubernetes: '/icons/connections/kubernetes-default.svg',
  'kubernetes-admin': '/icons/connections/kubernetes-default.svg',
  'kubernetes-exec': '/icons/connections/kubernetes-default.svg',
  'kubernetes-interactive': '/icons/connections/kubernetes-default.svg',
  'kubernetes-token': '/icons/connections/kubernetes-default.svg',
  npm: '/icons/connections/npm-default.svg',
  yarn: '/icons/connections/yarn-default.svg',
  docker: '/icons/connections/docker-default.svg',
  googlecloud: '/icons/connections/googlecloud-default.svg',
  helm: '/icons/connections/helm-default.svg',
  git: '/icons/connections/git-default.svg',
  github: '/icons/connections/github-default.svg',
  sentry: '/icons/connections/sentry-default.svg',
  django: '/icons/connections/django-default.svg',
  elixir: '/icons/connections/elixir-default.svg',
  cloudwatch: '/icons/connections/aws-cloudwatch-default.svg',
  dynamodb: '/icons/connections/aws-dynamodb-default.svg',
  bigquery: '/icons/connections/google-bigquery-default.svg',
  laravel: '/icons/connections/laravel-default.svg',
  cassandra: '/icons/connections/cassandra-default.svg',
  redis: '/icons/connections/redis-default.svg',
  grafana: '/icons/connections/grafana-default.svg',
  kibana: '/icons/connections/kibana-default.svg',
  'claude-code': '/icons/connections/claude-default.svg',
}

const COMMAND_TO_TYPE = {
  aws: 'aws',
  clj: 'clojure',
  docker: 'docker',
  'docker-compose': 'docker',
  gcloud: 'googlecloud',
  git: 'git',
  github: 'github',
  helm: 'helm',
  kubectl: 'kubernetes',
  mongosh: 'mongodb',
  mssql: 'mssql',
  mysql: 'mysql',
  node: 'nodejs',
  npm: 'npm',
  oci: 'oracledb',
  psql: 'postgres',
  python: 'python',
  python3: 'python',
  rails: 'ruby-on-rails',
  'sentry-cli': 'sentry',
  yarn: 'yarn',
  ssh: 'ssh',
  bash: 'custom',
  php: 'laravel',
  cqlsh: 'cassandra',
  'redis-cli': 'redis',
}

const FALLBACK = '/icons/connections/custom-ssh.svg'

/**
 * Returns the icon URL for a connection object.
 * Priority: subtype > command[0] > icon_name > type > fallback.
 * Mirrors CLJS get-connection-icon (connection-icons-default-dictionary).
 */
export function getConnectionIcon(connection) {
  const key =
    connection?.subtype ||
    (connection?.type === 'custom' && COMMAND_TO_TYPE[connection?.command?.[0]]) ||
    connection?.icon_name ||
    connection?.type ||
    'custom'

  return ICONS[key] ?? FALLBACK
}
