(ns webapp.resources.configure-role.details-tab
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [webapp.components.forms :as forms]
   [webapp.resources.configure-role.attributes-inputs :as attributes-inputs]
   [webapp.connections.views.setup.tags-inputs :as tags-inputs]))

(defn main [connection]
  [:> Box {:class "max-w-[600px] space-y-8"}
   [:> Box
    [forms/input {:label "Name"
                  :value (:name connection)
                  :disabled true}]]

   [attributes-inputs/main]
   [tags-inputs/main]])

