(ns webapp.resources.views.configure-role.details-tab
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text]]
   [webapp.components.forms :as forms]
   [webapp.connections.views.setup.tags-inputs :as tags-inputs]))

(defn main [connection]
  [:> Box {:class "max-w-[600px] space-y-8"}
   ;; Name (read-only)
   [:> Box
    [forms/input {:label "Name"
                  :value (:name connection)
                  :disabled true}]]

   [tags-inputs/main]])

