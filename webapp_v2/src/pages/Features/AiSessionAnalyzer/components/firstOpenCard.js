// The card of a CardStack that opens at first: the first still to configure,
// else the first.
export const firstOpenCard = (cards) => (cards.find((card) => !card.configured) ?? cards[0])?.key ?? null
