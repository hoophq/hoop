(ns webapp.webclient.aside.main
  (:require ["@headlessui/react" :as ui]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            [reagent.core :as r]
            [webapp.webclient.aside.database-schema :as database-schema]
            [webapp.webclient.aside.metadata :as metadata]
            [webapp.webclient.runbooks.list :as runbooks-list]))

(defn main
  [{:keys [show-tree? connection-name connection-type metadata metadata-key metadata-value schema-disabled?]}]

  [:aside {:class "h-full flex flex-col border-l border-gray-600"}
   [:> ui/Disclosure
    (fn [params]
      (r/as-element
       [:<>
        [:> (.-Button ui/Disclosure)
         {:className "h-6 w-full flex items-center gap-small text-xs text-white font-semibold bg-gray-800 hover:bg-gray-700 focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
         [:> hero-solid-icon/ChevronUpIcon {:class (str (when (.-open params) "rotate-180 transform ")
                                                        "text-white h-5 w-5 shrink-0")
                                            :aria-hidden "true"}]
         "Metadata"]

        [:> (.-Panel ui/Disclosure) {:className (str (when (.-open params)
                                                       "h-1/4 ")
                                                     "p-regular overflow-auto")}
         [metadata/main {:metadata metadata
                         :metadata-key metadata-key
                         :metadata-value metadata-value}]]]))]
   (when (and show-tree? (not schema-disabled?))
     [:> ui/Disclosure {:defaultOpen true}
      (fn [params]
        (r/as-element
         [:<>
          [:> (.-Button ui/Disclosure)
           {:class "h-6 w-full flex items-center gap-small text-xs text-white font-semibold bg-gray-800 hover:bg-gray-700 focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
           [:> hero-solid-icon/ChevronUpIcon {:class (str (when (.-open params) "rotate-180 transform ")
                                                          "text-white h-5 w-5 shrink-0")
                                              :aria-hidden "true"}]
           "Database Schema"]

          [:> (.-Panel ui/Disclosure) {:className "h-full p-regular overflow-auto"}
           [database-schema/main {:connection-name connection-name
                                  :connection-type connection-type}]]]))])

   [:> ui/Disclosure {:defaultOpen true}
    (fn [params]
      (r/as-element
       [:<>
        [:> (.-Button ui/Disclosure)
         {:class "h-6 w-full flex items-center gap-small text-xs text-white font-semibold bg-gray-800 hover:bg-gray-700 focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
         [:> hero-solid-icon/ChevronUpIcon {:class (str (when (.-open params) "rotate-180 transform ")
                                                        "text-white h-5 w-5 shrink-0")
                                            :aria-hidden "true"}]
         "Runbooks"]

        [:> (.-Panel ui/Disclosure) {:className "h-full p-regular overflow-auto"}
         [runbooks-list/main]]]))]])
