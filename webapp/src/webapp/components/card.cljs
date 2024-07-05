(ns webapp.components.card)

(defn main
  "Card component is a white box for receiving entire structures.
  Its size is defined by its parent component.
  children -> the children to be rendered
  inner-space? -> flag to add internal padding
  class -> CSS classes to be passed down to card container"
  [{:keys [children inner-space? class]}]
  [:div {:class (str (when inner-space? "px-regular py-small ")
                     (when class (str class " "))
                     "rounded-lg border border-gray-200 overflow-auto")}
   children])
