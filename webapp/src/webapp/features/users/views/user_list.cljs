(ns webapp.features.users.views.user-list
  (:require
   ["@radix-ui/themes" :refer [Button Table Text]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [webapp.components.tooltip :as tooltip]
   [webapp.features.users.views.user-form :as user-form]))

(defn user-row [user user-groups]
  [:> Table.Row {:key (:id user)}
   [:> Table.Cell {:width "25%" :maxWidth "260px"}
    [:> Text {:as "div" :size "2" :weight "medium" :class "text-gray-12"}
     (:name user)]]

   [:> Table.Cell {:width "25%" :maxWidth "260px"}
    [:> Text {:as "div" :size "2" :class "text-gray-12"}
     (:email user)]]

   [:> Table.Cell {:width "25%" :maxWidth "260px"}
    [:> Text {:size "2" :class "text-gray-12"}
     [tooltip/truncate-tooltip
      {:text (string/join ", " (:groups user))}]]]

   [:> Table.Cell {:width "15%"}
    [:> Text {:as "div" :size "2" :class "text-gray-12"}
     (:status user)]]

   [:> Table.Cell {:width "10%"}
    [:> Button {:size "2"
                :variant "ghost"
                :onClick #(rf/dispatch [:modal->open
                                        {:content [user-form/main :update user user-groups]
                                         :maxWidth "450px"}])}
     "Edit"]]])

(defn main []
  (let [users (rf/subscribe [:users])
        user-groups (rf/subscribe [:user-groups])]
    (fn []
      [:> Table.Root {:variant "surface"}
       [:> Table.Header
        [:> Table.Row
         [:> Table.ColumnHeaderCell "Name"]
         [:> Table.ColumnHeaderCell "Email"]
         [:> Table.ColumnHeaderCell "Groups"]
         [:> Table.ColumnHeaderCell "Status"]
         [:> Table.ColumnHeaderCell ""]]]

       [:> Table.Body
        (doall
         (for [user (sort-by #(string/lower-case (:name %)) @users)]
           ^{:key (:id user)}
           [user-row user @user-groups]))]])))
