(ns webapp.components.checkboxes
  (:require [clojure.string :as cs]))


;; Description: Renders a single checkbox input along with its label and optionally a description.
;; Parameters:
;; - A map with the following keys:
;;   - :name (String): The `name` attribute for the checkbox input element. Used as a unique identifier.
;;   - :label (String): The text displayed next to the checkbox. This text is capitalized and displayed as the content of the label.
;;   - :description (String, optional): Additional description for the checkbox. If provided, displayed below the label in smaller text.
;;   - :checked? (Atom, Boolean): An atom that indicates whether the checkbox is checked (`true`) or not (`false`).
(defn item [{:keys [name label description disabled? checked?]}]
  [:div {:class (str "relative flex items-center"
                     (when disabled? " cursor-not-allowed"))}
   [:div {:class "flex h-6 items-center"}
    [:input {:id name
             :name name
             :type "checkbox"
             :disabled disabled?
             :aria-describedby (str name "-description")
             :checked @checked?
             :on-change #(swap! checked? not)
             :class (str "h-4 w-4 rounded border-gray-300 text-blue-500 focus:ring-blue-500"
                         (when disabled? " cursor-not-allowed opacity-50"))}]]
   [:div {:class "ml-3 text-sm leading-6"}
    [:label {:for name
             :class (str (if (or (not @checked?) disabled?) "text-gray-500 " "text-gray-900 ")
                         "font-semibold font-medium")}
     (str (cs/capitalize label))]
    (when description
      [:p {:id (str name "-description")
           :class (str (if (or (not @checked?) disabled?) "text-gray-400 " "text-gray-500 ")
                       "text-xs")}
       description])]])


;; Description: Renders a group of checkboxes as a fieldset.
;; Parameters:
;; - checkboxes: A collection of maps, each representing a checkbox. Each map must contain keys for the `checkbox` function, such as `:name`, `:label`, `:description`, and `:checked?`.
(defn group [checkboxes]
  [:fieldset
   [:legend {:class "sr-only"}
    "Notifications"]
   [:div {:class "space-y-5"}
    (for [checkbox checkboxes]
      ^{:key (:name checkbox)}
      [item checkbox])]])
