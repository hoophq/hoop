(ns webapp.components.command-dialog
  (:require
   ["cmdk" :refer [CommandDialog CommandInput CommandList CommandLoading]]
   ["@radix-ui/themes" :refer [Box Flex Text Spinner ScrollArea]]
   ["lucide-react" :refer [Search X]]
   [webapp.components.theme-provider :refer [theme-provider]]))

(defn breadcrumb-tag
  "Tag showing current context"
  [{:keys [current-page context on-close]}]
  (let [label (case current-page
                :resource-roles (:name context)
                :connection-actions (:name context)
                (str current-page))]
    [:> Flex
     {:align "center"
      :gap "2"
      :class "bg-gray-3 px-2 py-1 rounded-full"}
     [:> Text
      {:size "1"
       :weight "medium"
       :class "text-[--gray-11]"}
      label]
     (when on-close
       [:button {:class "hover:bg-gray-5 rounded p-0.5 transition-colors"
                 :on-click on-close}
        [:> X {:size 12}]])]))

(defn command-dialog
  "Reusable command dialog component"
  [{:keys [open? on-open-change title max-width height class-name breadcrumb-component
           search-config breadcrumb-config content children loading? should-filter?]
    :or {title "Command Dialog"
         max-width "max-w-2xl"
         height "h-96"
         class-name ""
         should-filter? false}}]
  [:> CommandDialog
   (merge
    {:shouldFilter should-filter?} ;; Use manual filtering for async search (false) or native filtering (true)
    (when (:on-key-down search-config)
      {:onKeyDown (:on-key-down search-config)})
    {:open open?
     :label title
     :onOpenChange on-open-change
     :className "fixed inset-0 z-50 flex items-start justify-center pt-[20vh]"})
   [theme-provider
    [:<>
     ;; Manual overlay for click outside with blur effect
     [:> Box {:class "fixed inset-0 bg-black/10 backdrop-blur-sm"
              :on-click #(when on-open-change (on-open-change false))}]

     [:> Box
      {:class (str "w-[96vw] "
                   max-width " bg-white rounded-lg shadow-2xl border border-gray-6 overflow-hidden "
                   height " flex flex-col relative z-10 "
                   class-name)}
      (when search-config
        [:> Flex
         {:align "center"
          :gap "3"
          :class "px-4 py-3 border-b border-gray-6"}
         (when (:show-search-icon search-config)
           [:> Search {:size 16
                       :class (str "transition-colors duration-200 "
                                   (if (:is-searching? search-config)
                                     "text-blue-9"
                                     "text-gray-11"))}])
         [:> Flex
          {:align "center"
           :gap "2"
           :class "flex-1"}
          (when (:show-input search-config)
            [:> CommandInput
             {:placeholder (:placeholder search-config "Search...")
              :value (or (:value search-config) "")
              :className "flex-1 bg-transparent border-none outline-none text-sm placeholder:text-gray-11"
              :onValueChange (:on-value-change search-config)}])

          (when breadcrumb-component
            [breadcrumb-component])

          (when breadcrumb-config
            [breadcrumb-tag {:current-page (:current-page breadcrumb-config)
                             :context (:context breadcrumb-config)
                             :on-close (:on-close breadcrumb-config)}])]])
      (if loading?
        [:> CommandLoading {:className "flex-1 flex items-center justify-center p-4"}
         [:> Flex {:align "center" :gap "2"}
          [:> Spinner {:size "2"}]
          [:> Text {:size "2" :class "text-gray-12"}
           "Loading..."]]]

        [:> ScrollArea {:scrollbars "vertical"
                        :size "2"}
         [:> CommandList
          {:className "flex-1 overflow-y-auto p-4"}
          (or content children)]])]]]])
