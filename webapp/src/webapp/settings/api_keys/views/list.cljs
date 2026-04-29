(ns webapp.settings.api-keys.views.list
  (:require
   ["@radix-ui/themes" :refer [Box DropdownMenu Flex IconButton Table Text Tooltip]]
   ["lucide-react" :refer [EllipsisVertical KeyRound Unplug]]
   [re-frame.core :as rf]))

(defn- formatted-date [date-str]
  (when date-str
    (-> (js/Date. date-str)
        (.toLocaleDateString "en-US" #js {:year "numeric" :month "short" :day "numeric"}))))

(defn- truncate-key [key-str]
  (if (and key-str (> (count key-str) 12))
    (str (subs key-str 0 10) "...")
    key-str))

(defn main []
  (let [api-keys (rf/subscribe [:api-keys/list-data])]
    (fn []
      [:> Table.Root {:variant "surface"}
       [:> Table.Header
        [:> Table.Row
         [:> Table.ColumnHeaderCell {:width "16.67%"} "Key"]
         [:> Table.ColumnHeaderCell {:width "16.67%"} "Name"]
         [:> Table.ColumnHeaderCell {:width "16.67%"} "Created at"]
         [:> Table.ColumnHeaderCell {:width "16.67%"} "Created by"]
         [:> Table.ColumnHeaderCell {:width "16.67%"} "Last used at"]
         [:> Table.ColumnHeaderCell {:width "16.67%"} ""]]]

       [:> Table.Body
        (doall
         (for [ak @api-keys]
           ^{:key (:id ak)}
           [:> Table.Row
            [:> Table.Cell
             (let [revoked? (= (:status ak) "revoked")]
               (if revoked?
                 [:> Tooltip {:content "Deactivated key" :side "top"}
                  [:> Flex {:align "center" :gap "2" :class "cursor-default"}
                   [:> Unplug {:size 14 :class "text-[--gray-8] shrink-0"}]
                   [:> Text {:class "font-mono text-sm text-[--gray-8]"}
                    (truncate-key (:masked_key ak))]]]
                 [:> Flex {:align "center" :gap "2"}
                  [:> KeyRound {:size 14 :class "text-[--gray-9] shrink-0"}]
                  [:> Text {:class "font-mono text-sm"} (truncate-key (:masked_key ak))]]))]
            [:> Table.Cell (:name ak)]
            [:> Table.Cell (formatted-date (:created_at ak))]
            [:> Table.Cell (:created_by ak)]
            [:> Table.Cell (or (formatted-date (:last_used_at ak)) "—")]
            [:> Table.Cell {:justify "end"}
             [:> DropdownMenu.Root {:dir "rtl"}
              [:> DropdownMenu.Trigger
               [:> IconButton {:size "1"
                               :variant "ghost"
                               :color "gray"
                               :aria-label (str "More options for " (:name ak))}
                [:> EllipsisVertical {:size 16}]]]
              [:> DropdownMenu.Content
               (if (= (:status ak) "active")
                 [:<>
                  [:> DropdownMenu.Item
                   {:on-click #(rf/dispatch [:navigate :settings-api-keys-configure {} :id (:id ak)])}
                   "Configure"]
                  [:> DropdownMenu.Item
                   {:color "red"
                    :on-click #(rf/dispatch [:dialog->open
                                             {:title "Deactivate API key"
                                              :text (str "Are you sure you want to deactivate '" (:name ak) "'? You can reactivate it later.")
                                              :text-action-button "Deactivate"
                                              :action-button? true
                                              :type :danger
                                              :on-success (fn []
                                                            (rf/dispatch [:api-keys/revoke (:id ak)]))}])}
                   "Deactivate API key"]]
                 [:<>
                  [:> DropdownMenu.Item
                   {:on-click #(rf/dispatch [:navigate :settings-api-keys-configure {} :id (:id ak)])}
                   "Configure"]
                  [:> DropdownMenu.Item
                   {:on-click #(rf/dispatch [:api-keys/activate (:id ak)])}
                   "Activate API key"]])]]]]))]])))
