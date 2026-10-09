import { Text } from "@mantine/core";
import { TriangleAlert } from "lucide-react";
import Alert from "@/components/Alert";

/**
 * Shown on a sidecar whose analyzer uses the model Hoop hosts
 * (`analyzer.use_hoop_llm_provider`). The sidecar logs the same warning at
 * startup; this puts it where an admin of the control plane looks.
 */
export default function HostedAnalyzerNotice({ configuration }) {
  if (!configuration?.analyzer?.use_hoop_llm_provider) return null;

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
        sidecar's analyzer section.
      </Text>
    </Alert>
  );
}
