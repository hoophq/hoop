(ns webapp.webclient.components.federation-connect-callout
  "Prompts the signed-in user to connect their per-user account (e.g. their
  Google account for the gcp_oauth federation provider) before running a
  federated resource. Rendered by the editor in two situations:

   - proactively, when the connection's OAuth status reports the user has not
     connected yet (see :federation/oauth-status); and
   - reactively, when a run fails with the backend's stable
     `code=oauth_not_connected` marker.

  The Connect button starts the OAuth consent flow via :federation/oauth-connect
  which redirects the browser to Google and returns to this page afterwards."
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   ["lucide-react" :refer [KeyRound]]
   [re-frame.core :as rf]))

(defn main [{:keys [connection-name title description]}]
  [:> Flex {:justify "between" :align "start" :gap "3"
            :class "absolute top-3 right-3 z-50 max-w-[320px] pointer-events-auto p-4 rounded-4 bg-warning-1 shadow-lg"}
   [:> Flex {:gap "2" :align "start"}
    [:> Box {:class "shrink-0 mt-[2px] text-warning-12"}
     [:> KeyRound {:size 16}]]
    [:> Flex {:direction "column" :gap "2"}
     [:> Text {:as "p" :size "2" :weight "bold" :class "text-warning-12"}
      (or title "Connect your Google account")]
     [:> Text {:as "p" :size "1" :class "text-warning-12"}
      (or description
          (str "This resource runs queries as you. Connect your Google account "
               "once to authorize access, then run again."))]
     [:> Button {:size "1"
                 :variant "solid"
                 :color "amber"
                 :type "button"
                 :on-click #(rf/dispatch [:federation/oauth-connect connection-name])}
      "Connect Google account"]]]])
