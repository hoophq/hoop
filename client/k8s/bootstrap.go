package k8s

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/homedir"

	// load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

const (
	DefaultNamespaceName          = "hoopstart"
	DefaultClusterRoleBindingName = "hoop:system:token-granter"
	DefaultClusterRole            = "view"
	defaultServiceAccountName     = "token-granter"
)

type TokenGranterOptions struct {
	Namespace         string
	KubeconfigContext string
	Expiration        time.Duration
	ClusterRole       string
}

func BootstrapTokenGranter(opts *TokenGranterOptions) (string, error) {
	cmdapiConfig, err := getClientCmdAPIConfig()
	if err != nil {
		return "", err
	}

	if opts.KubeconfigContext == "" {
		opts.KubeconfigContext = cmdapiConfig.CurrentContext
	}
	if opts.Namespace == "" {
		opts.Namespace = DefaultNamespaceName
	}
	if opts.ClusterRole == "" {
		opts.ClusterRole = DefaultClusterRole
	}

	clientset, err := getK8sClientSet(opts.KubeconfigContext)
	if err != nil {
		return "", err
	}
	base64CA, apiServerAddress, err := getClusterData(cmdapiConfig, opts.KubeconfigContext)
	if err != nil {
		return "", err
	}
	if err := createNamespace(clientset, opts.Namespace); err != nil {
		return "", fmt.Errorf("failed creating namespace, err=%v", err)
	}
	if err := createClusterRoleBinding(clientset, opts.ClusterRole, opts.Namespace); err != nil {
		return "", fmt.Errorf("failed creating cluster role binding, err=%v", err)
	}
	if err := createServiceAccount(clientset, opts.Namespace); err != nil {
		return "", fmt.Errorf("failed creating service account, err=%v", err)
	}

	accessToken, err := createServiceAccountToken(clientset, opts.Namespace, &opts.Expiration)
	if err != nil {
		return "", fmt.Errorf("failed creating service account token, err=%v", err)
	}
	return parseKubeconfigTemplate(apiServerAddress, accessToken, base64CA), nil
}

var kubeconfigTemplate = `apiVersion: v1
kind: Config
clusters:
- name: default-cluster
  cluster:
    certificate-authority-data: %s
    server: %s
contexts:
- name: default-context
  context:
    cluster: default-cluster
    user: default-user
current-context: default-context
users:
- name: default-user
  user:
    token: %s
`

func parseKubeconfigTemplate(apiServerAddress, accessToken, base64CA string) string {
	return fmt.Sprintf(kubeconfigTemplate, base64CA, apiServerAddress, accessToken)
}

func createNamespace(clientset *kubernetes.Clientset, namespace string) error {
	_, err := clientset.CoreV1().Namespaces().Create(
		context.Background(),
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
			Spec:       v1.NamespaceSpec{}},
		metav1.CreateOptions{},
	)
	if errors.IsAlreadyExists(err) || err == nil {
		return nil
	}
	return err
}

func createClusterRoleBinding(clientset *kubernetes.Clientset, clusterrole, namespace string) error {
	_, err := clientset.RbacV1().ClusterRoleBindings().Create(
		context.Background(),
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: DefaultClusterRoleBindingName},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole", // does it have an constant for that?
				Name:     clusterrole,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      rbacv1.ServiceAccountKind,
					Name:      defaultServiceAccountName,
					Namespace: namespace,
				},
			},
		},
		metav1.CreateOptions{},
	)
	if errors.IsAlreadyExists(err) || err == nil {
		return nil
	}
	return err
}

func createServiceAccount(clientset *kubernetes.Clientset, namespace string) error {
	_, err := clientset.CoreV1().ServiceAccounts(namespace).Create(
		context.Background(),
		&v1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name: defaultServiceAccountName,
			},
		},
		metav1.CreateOptions{},
	)
	if errors.IsAlreadyExists(err) || err == nil {
		return nil
	}
	return err
}

func createServiceAccountToken(clientset *kubernetes.Clientset, namespace string, expiration *time.Duration) (string, error) {
	tokenRequest, err := clientset.CoreV1().ServiceAccounts(namespace).CreateToken(
		context.Background(),
		defaultServiceAccountName,
		&authenticationv1.TokenRequest{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
			},
			Spec: authenticationv1.TokenRequestSpec{},
		},
		metav1.CreateOptions{},
	)
	if *expiration > 0 {
		tokenRequest.Spec.ExpirationSeconds = (*int64)(expiration)
	}
	if err != nil {
		return "", err
	}
	return tokenRequest.Status.Token, nil
}

func getKubeConfigPath() (string, error) {
	kubeconfigHomeDir := homedir.HomeDir()
	if kubeconfigHomeDir == "" {
		return "", fmt.Errorf("kubeconfig home dir is empty")
	}
	return filepath.Join(kubeconfigHomeDir, ".kube", "config"), nil
}

func getClientCmdAPIConfig() (*clientcmdapi.Config, error) {
	kubeconfigPath, err := getKubeConfigPath()
	if err != nil {
		return nil, nil
	}
	conf, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		&clientcmd.ConfigOverrides{
			CurrentContext: "",
		}).RawConfig()
	return &conf, err
}

func getK8sClientSet(ctxname string) (*kubernetes.Clientset, error) {
	kubeconfigHomeDir := homedir.HomeDir()
	if kubeconfigHomeDir == "" {
		return nil, fmt.Errorf("kubeconfig home dir is empty")
	}
	kubeconfig := filepath.Join(kubeconfigHomeDir, ".kube", "config")
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{
			CurrentContext: ctxname,
		}).ClientConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

// getClusterData retrieves the certificate authority as a base64 value and the api server address
func getClusterData(config *clientcmdapi.Config, contextKey string) (string, string, error) {
	cluster, ok := config.Clusters[contextKey]
	if !ok {
		return "", "", fmt.Errorf("failed to retrieve CA from context %v", contextKey)
	}
	if cluster.CertificateAuthority != "" {
		ca, err := os.ReadFile(cluster.CertificateAuthority)
		if err != nil {
			return "", "", fmt.Errorf("failed reading CA from file %v, err=%v", cluster.CertificateAuthority, err)
		}

		return base64.StdEncoding.EncodeToString(ca), cluster.Server, nil
	}
	if len(cluster.CertificateAuthorityData) == 0 {
		return "", "", fmt.Errorf("certificate-authority-data or certificate-authority cluster config are empty")
	}
	return base64.StdEncoding.EncodeToString(cluster.CertificateAuthorityData), cluster.Server, nil
}
