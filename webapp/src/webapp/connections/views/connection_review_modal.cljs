(ns webapp.connections.views.connection-review-modal
  (:require ["@radix-ui/themes" :refer [Box Button Flex Heading Text Link]]
            ["lucide-react" :refer [ExternalLink]]
            [re-frame.core :as rf]
            [webapp.components.button :as button]
            [webapp.routes :as routes]))

(defn main [connection]
  [:> Box
   [:header {:class "mb-4"}
    [:> Heading {:size "6" :as "h2"}
     "Hoop Access Review Required"]]

   [:main {:class "space-y-5"}
    [:> Text {:as "p" :size "2"}
     "Your access request to "
     [:span {:class "font-bold"}
      (:connection_name connection)]
     " requires approval by a reviewer."]

    [:> Text {:as "p" :size "2"}
     "You can check the status of your request at any time by clicking the link below or accessing the session details:"]

    [:> Flex {:align "center" :gap "2" :class "mt-4 p-3 bg-blue-50 rounded-md"}
     [:> Link {:href (str (-> js/document .-location .-origin)
                          (routes/url-for :sessions)
                          "/" (:session_id connection))
               :target "_blank"
               :class "text-blue-600 flex items-center gap-2"}
      "View session details"
      [:> ExternalLink {:size 16}]]]]

   [:footer {:class "mt-6 flex justify-end"}
    [:> Button {:on-click #(rf/dispatch [:modal->close])}
     "Close"]]])
