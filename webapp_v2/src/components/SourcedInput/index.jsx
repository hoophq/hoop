import MarkdownText from '@/components/MarkdownText'
import SingleOutlineSourcedInput from './SingleOutline'
import GluedSiblingsSourcedInput from './GluedSiblings'
import {
  useSourcedInputVariant,
  VARIANT_GLUED_SIBLINGS,
} from './variantContext'

// Input field with an optional credential source picker (Manual /
// Vault KV / AWS Secrets Manager / AWS IAM Role) rendered as a left
// adornment. The picker shows a lucide icon and label per source — see
// SOURCE_ICONS in pages/Roles/Configure/utils/secretsCodec.js.
//
// Two variants exist behind the SourcedInputVariantContext:
//
//   * single-outline (default, CLJS-like) — picker + value + right
//     adornment inside one outlined cell. Built with custom composition
//     around an Input.Input core so Mantine's leftSection bug from
//     commit ecd2268 (label overlapping placeholder) can't recur.
//
//   * glued-siblings — picker and input as two touching Mantine
//     components with matching radii and a shared seam border.
//
// A temporary Switch at the top of CredentialsTab.jsx flips the variant
// for everyone on the page. After the design choice is locked in, the
// loser is deleted and the dispatch goes away.
//
// `description` is rendered between the label and the input via
// MarkdownText (auto-converts `[text](url)` to anchors). The label sits
// outside any input wrapper so the source adornment slot stays clean —
// each variant renders its own label.
export default function SourcedInput(props) {
  const variant = useSourcedInputVariant()
  const Variant =
    variant === VARIANT_GLUED_SIBLINGS
      ? GluedSiblingsSourcedInput
      : SingleOutlineSourcedInput

  const descriptionSlot = props.description ? (
    <MarkdownText>{props.description}</MarkdownText>
  ) : null

  return <Variant {...props} descriptionSlot={descriptionSlot} />
}
