import { useMemo, useState } from "react";
import { Box, Flex, Popover, Stack, Text } from "@mantine/core";
import { Check, Search, X } from "lucide-react";
import Button from "@/components/Button";
import TextInput from "@/components/TextInput";

/**
 * A popover-based single-select filter. Shows a searchable list of values
 * with optional clear. Designed for table toolbar filter bars.
 *
 * Props:
 *   icon      — lucide-react icon component
 *   label     — display label (e.g. "Resource", "Type")
 *   values    — string[] of available options
 *   selected  — currently selected value (string | null)
 *   onSelect  — (value: string) => void
 *   onClear   — () => void
 */
export default function FilterPopover({
	icon,
	label,
	values,
	selected,
	onSelect,
	onClear,
}) {
	const [open, setOpen] = useState(false);
	const [searchTerm, setSearchTerm] = useState("");
	const Icon = icon;

	const hasSelected = typeof selected === "string" && selected.trim() !== "";
	const filtered = useMemo(() => {
		const q = searchTerm.trim().toLowerCase();
		if (!q) return values;
		return values.filter((v) => v.toLowerCase().includes(q));
	}, [values, searchTerm]);

	const close = () => {
		setOpen(false);
		setSearchTerm("");
	};

	return (
		<Popover
			opened={open}
			onChange={setOpen}
			position="bottom-start"
			width={320}
			withinPortal
		>
			<Popover.Target>
				<Button
					variant={hasSelected ? "light" : "default"}
					color="gray"
					onClick={() => setOpen((value) => !value)}
					leftSection={<Icon size={16} />}
					rightSection={
						hasSelected ? (
							<X
								size={14}
								onClick={(event) => {
									event.stopPropagation();
									onClear();
									close();
								}}
							/>
						) : null
					}
				>
					{hasSelected ? selected : label}
				</Button>
			</Popover.Target>
			<Popover.Dropdown p="xs">
				<Stack gap="xs">
					{hasSelected && (
						<Box
							px="sm"
							py="xs"
							onClick={() => {
								onClear();
								close();
							}}
							style={{ cursor: "pointer", borderRadius: 4 }}
						>
							<Text size="sm" c="dimmed">
								Clear filter
							</Text>
						</Box>
					)}
					<TextInput
						placeholder={`Search ${label.toLowerCase()}`}
						value={searchTerm}
						onChange={(event) => setSearchTerm(event.currentTarget.value)}
						leftSection={<Search size={14} />}
						size="xs"
					/>
					{filtered.length > 0 ? (
						<Stack gap={0} mah={288} style={{ overflowY: "auto" }}>
							{filtered.map((value) => (
								<Flex
									key={value}
									align="center"
									justify="space-between"
									px="sm"
									py="xs"
									onClick={() => {
										onSelect(value);
										close();
									}}
									style={{ cursor: "pointer", borderRadius: 4 }}
								>
									<Text size="sm" lineClamp={1}>
										{value}
									</Text>
									{value === selected && <Check size={14} />}
								</Flex>
							))}
						</Stack>
					) : (
						<Box px="sm" py="md">
							<Text size="xs" c="dimmed" fs="italic">
								{searchTerm
									? `No ${label.toLowerCase()} found`
									: `No ${label.toLowerCase()} available`}
							</Text>
						</Box>
					)}
				</Stack>
			</Popover.Dropdown>
		</Popover>
	);
}
