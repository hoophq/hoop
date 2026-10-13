import { Text } from "@mantine/core";
import { TriangleAlert } from "lucide-react";
import Alert from "@/components/Alert";
import { useUserStore } from "@/stores/useUserStore";

// Mirrors AnalyzerConfig.effectiveAnalyzer in the sidecar daemon: the hosted
// model runs when use_hoop_llm_provider is true, or when it is unset, no
// provider is named, a listener analyses and there is no Enterprise license.
function usesHostedModel(configuration, isFreeLicense) {
  const flag = configuration?.analyzer?.use_hoop_llm_provider;
  if (flag !== undefined) return flag === true;
  if (configuration?.analyzer?.provider) return false;
  const analyses = (configuration?.listeners ?? []).some((l) => l.analyzer);
  return analyses && isFreeLicense;
}

/**
 * Shown on a sidecar whose analyzer uses the model Hoop hosts. The sidecar
 * logs the same warning at startup; this puts it where an admin of the control
 * plane looks.
 */
export default function HostedAnalyzerNotice({ configuration }) {
  const isFreeLicense = useUserStore((s) => s.isFreeLicense);
  if (!usesHostedModel(configuration, isFreeLicense)) return null;

  return (
    <Alert
      color="yellow"
      variant="light"
      icon={<TriangleAlert size={16} />}
      radius="md"
    >
      <Text size="sm">
        This sidecar's AI analyzer uses the model Hoop hosts. Statement text,
        which can contain personal data, is sent to Hoop for classification. To
        keep it in your network, set your own provider and model in the
        sidecar's analyzer section, or set use_hoop_llm_provider to false.
      </Text>
    </Alert>
  );
}
