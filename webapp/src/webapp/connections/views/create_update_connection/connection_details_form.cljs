(ns webapp.connections.views.create-update-connection.connection-details-form
  (:require ["@radix-ui/themes" :refer [Box Callout Flex Grid Link Switch Text]]
            ["lucide-react" :refer [ArrowUpRight Star]]
            [clojure.string :as s]
            [webapp.components.forms :as forms]
            [webapp.components.multiselect :as multi-select]
            [webapp.connections.dlp-info-types :as dlp-info-types]))

(defn array->select-options [array]
  (mapv #(into {} {"value" % "label" (s/lower-case (s/replace % #"_" " "))}) array))

(defn access-mode-exec-disabled? [connection-type connection-subtype]
  (cond
    (and (= connection-type "application")
         (= connection-subtype "tcp")) true
    (and (= connection-type "custom")
         (= connection-subtype "ssh")) true
    :else false))

(defn access-mode-connect-disabled? [connection-type connection-subtype]
  (cond
    (and (= connection-type "database")
         (= connection-subtype "oracledb")) true
    :else false))

(defn access-mode-runbooks-disabled? [connection-type connection-subtype]
  (cond
    (and (= connection-type "application")
         (= connection-subtype "tcp")) true
    (and (= connection-type "custom")
         (= connection-subtype "ssh")) true
    :else false))

(defn main
  [{:keys [user-groups
           free-license?
           connection-name
           connection-type
           connection-subtype
           connection-tags-value
           connection-tags-input-value
           form-type
           reviews
           review-groups
           ai-data-masking
           ai-data-masking-info-types
           enable-database-schema
           access-mode-runbooks
           access-mode-exec
           access-mode-connect]}]
  [:> Flex {:direction "column" :gap "9" :class "px-20"}
   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Connection information"]
     [:> Text {:size "3" :class "text-gray-11"} "Names are used to identify your connection and can't be changed."]]
    [:> Box {:class "space-y-radix-5" :grid-column "span 3 / span 3"}
     [forms/input {:placeholder "mssql-armadillo-9696"
                   :label "Name"
                   :required true
                   :disabled (= form-type :update)
                   :value @connection-name
                   :on-change #(reset! connection-name (-> % .-target .-value))}]
     [multi-select/text-input {:value @connection-tags-value
                               :input-value @connection-tags-input-value
                               :on-change (fn [value]
                                            (reset! connection-tags-value value))
                               :on-input-change (fn [value]
                                                  (reset! connection-tags-input-value value))
                               :label "Tags"
                               :id "tags-multi-select-text-input"
                               :name "tags-multi-select-text-input"}]]]
   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Configuration parameters"]
     [:> Text {:size "3" :class "text-gray-11"} "Setup how users interact with this connection."]
     [:> Link {:href "https://hoop.dev/docs/learn/jit-reviews"
               :target "_blank"}
      [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
       [:> Callout.Icon
        [:> ArrowUpRight {:size 16}]]
       [:> Callout.Text
        "Learn more about Reviews"]]]

     [:> Link {:href "https://hoop.dev/docs/learn/ai-data-masking"
               :target "_blank"}
      [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
       [:> Callout.Icon
        [:> ArrowUpRight {:size 16}]]
       [:> Callout.Text
        "Learn more about AI Data Masking"]]]]

    [:> Flex {:direction "column" :gap "7" :grid-column "span 3 / span 3"}
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @reviews
                   :size "3"
                   :onCheckedChange #(reset! reviews %)}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Reviews"]
        [:> Text {:as "p" :size "2"} (str "Require approval prior to connection execution. "
                                          "Enable Just-in-Time access for 30-minute sessions or Command reviews "
                                          "for individual query approvals.")]
        (when @reviews
          [:> Box {:mt "4"}
           [multi-select/main {:options (array->select-options @user-groups)
                               :id "approval-groups-input"
                               :name "approval-groups-input"
                               :required? @reviews
                               :default-value (if @reviews
                                                @review-groups
                                                nil)
                               :on-change #(reset! review-groups (js->clj %))}]])]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @ai-data-masking
                   :size "3"
                   :onCheckedChange #(reset! ai-data-masking %)
                   :disabled free-license?}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "AI Data Masking"]
        [:> Text {:as "p" :size "2"} (str "Provide an additional layer of security by ensuring "
                                          "sensitive data is masked in query results with AI-powered data masking.")]
        (when free-license?
          [:> Callout.Root {:size "2" :mt "4" :mb "4"}
           [:> Callout.Icon
            [:> Star {:size 16}]]
           [:> Callout.Text {:class "text-gray-12"}
            "Enable AI Data Masking by "
            [:> Link {:href "#"
                      :class "text-primary-10"
                      :on-click #(js/window.Intercom
                                  "showNewMessage"
                                  "I want to upgrade my current plan")}
             "upgrading your plan."]]])
        (when @ai-data-masking
          [:> Box {:mt "4"}
           [multi-select/main {:options (array->select-options dlp-info-types/options)
                               :id "data-masking-groups-input"
                               :name "data-masking-groups-input"
                               :disabled? (or (not @ai-data-masking) free-license?)
                               :required? @ai-data-masking
                               :default-value (if @ai-data-masking
                                                @ai-data-masking-info-types
                                                nil)
                               :on-change #(reset! ai-data-masking-info-types (js->clj %))}]])]]]
     (when (= "database" @connection-type)
       [:> Box {:class "space-y-radix-5"}
        [:> Flex {:align "center" :gap "5"}
         [:> Switch {:checked @enable-database-schema
                     :size "3"
                     :onCheckedChange #(reset! enable-database-schema %)}]
         [:> Box
          [:> Text {:as "h4" :size "3" :weight "medium"} "Database schema"]
          [:> Text {:as "p" :size "2"} "Show database schema in the Editor section."]]]])]]

   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Access modes"]
     [:> Text {:size "3" :class "text-gray-11"} "Setup how users interact with this connection."]]
    [:> Flex {:direction "column" :gap "7" :grid-column "span 3 / span 3"}
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @access-mode-runbooks
                   :size "3"
                   :disabled (access-mode-runbooks-disabled? @connection-type @connection-subtype)
                   :onCheckedChange #(reset! access-mode-runbooks %)}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Runbooks"]
        [:> Text {:as "p" :size "2"} "Create templates to automate tasks in your organization. "]]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @access-mode-connect
                   :size "3"
                   :disabled (access-mode-connect-disabled? @connection-type @connection-subtype)
                   :onCheckedChange #(reset! access-mode-connect %)}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Native"]
        [:> Text {:as "p" :size "2"} (str "Access from your client of preference using hoop.dev to channel "
                                          "connections using our Desktop App or our Command Line Interface.")]]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @access-mode-exec
                   :size "3"
                   :disabled (access-mode-exec-disabled? @connection-type @connection-subtype)
                   :onCheckedChange #(reset! access-mode-exec %)}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Web and one-offs"]
        [:> Text {:as "p" :size "2"} (str "Use hoop.dev's developer portal or our "
                                          "CLI's One-Offs commands directly in your terminal.")]]]]]]])
